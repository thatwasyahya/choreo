import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/sync/sync_api.dart';
import 'package:drift/drift.dart';

/// What one round trip did.
class SyncReport {
  const SyncReport({
    this.pushed = 0,
    this.settled = 0,
    this.applied = 0,
    this.quarantined = 0,
    this.cursorSeq = 0,
    this.snapshotRequired = false,
    this.hasMore = false,
  });

  final int pushed;
  final int settled;
  final int applied;
  final int quarantined;
  final int cursorSeq;
  final bool snapshotRequired;
  final bool hasMore;
}

/// The loop: drain the outbox, apply what comes back.
///
/// Three rules shape every line of it, and each one closes a way the app could
/// quietly lie to its user:
///
/// **The cursor moves only on what SQLite has actually committed.** The page is
/// applied and the cursor advanced in ONE transaction, so a crash halfway
/// through resumes where it stopped rather than leaving half a page on screen
/// and a cursor past it.
///
/// **A row we cannot read goes aside and the cursor moves on anyway.** One
/// unreadable row must never freeze a phone's whole synchronisation in silence.
/// That is also what makes an older app survive a newer server: an entity it
/// has never heard of is set aside, not fatal.
///
/// **A server state never overwrites a field the user is still editing.** Every
/// row carries `pendingFields`; applying a state writes everything except
/// those. It is the only reliable defence against synchronisation revoking the
/// sentence somebody is in the middle of typing.
class SyncEngine {
  SyncEngine({required this.db, required this.api, this.now});

  final AppDatabase db;
  final SyncApi api;

  /// Injectable monotonic source, for tests. Never a wall clock: backoff and
  /// queue age computed from one go haywire the moment the clock is corrected.
  final int Function()? now;

  static const int _batchLimit = 200;

  int get _elapsed => now?.call() ?? DateTime.now().millisecondsSinceEpoch;

  Future<SyncReport> syncOnce(String householdId, {int waitMs = 0}) async {
    final identity = await db.select(db.deviceIdentity).getSingleOrNull();
    final token = identity?.accessToken;
    if (token == null || token.isEmpty) {
      throw const SyncException(SyncFailure.unauthenticated, 'no access token');
    }

    final cursor = await db.cursorFor(householdId);
    final batch = await _takeBatch(householdId);

    final response = await api.sync(
      householdId: householdId,
      accessToken: token,
      cursorSeq: cursor?.cursorSeq ?? 0,
      ops: batch.map(_wire).toList(),
      waitMs: waitMs,
    );

    return _apply(householdId, batch, response);
  }

  /// The oldest gestures first, and never more than one batch.
  ///
  /// Ordered by lseq, which is an autoincrement: on a phone it is the only
  /// monotonic thing, and ordering a replay by a wall clock would reorder it
  /// the moment the network corrects the time.
  Future<List<OutboxData>> _takeBatch(String householdId) {
    return (db.select(db.outbox)
          ..where((o) => o.householdId.equals(householdId))
          ..orderBy([(o) => OrderingTerm.asc(o.lseq)])
          ..limit(_batchLimit))
        .get();
  }

  Map<String, dynamic> _wire(OutboxData entry) {
    return {
      'op_id': entry.opId,
      'kind': entry.kind,
      'entity': entry.entity,
      'row_id': entry.rowId,
      'fields': jsonDecode(entry.payload),
      'base_revs': jsonDecode(entry.baseRevs),
      if (entry.clientTs != null)
        'client_ts': DateTime.fromMillisecondsSinceEpoch(
          entry.clientTs!,
          isUtc: true,
        ).toIso8601String(),
    };
  }

  Future<SyncReport> _apply(
    String householdId,
    List<OutboxData> batch,
    SyncResponse response,
  ) async {
    var settled = 0;
    var applied = 0;
    var quarantined = 0;

    // One transaction for the whole page, cursor included. That is what makes
    // "resume where it stopped" true rather than hopeful.
    await db.transaction(() async {
      final sent = {for (final entry in batch) entry.opId: entry};

      for (final result in response.results) {
        final entry = sent[result.opId];
        if (entry == null || !result.isSettled) {
          // Retryable, or not ours. Either way it stays in the queue.
          continue;
        }

        await _clearPending(entry);
        await (db.delete(
          db.outbox,
        )..where((o) => o.opId.equals(result.opId))).go();
        settled++;
      }

      for (final change in response.changes) {
        try {
          await _applyChange(householdId, change);
          applied++;
        } on Exception catch (error) {
          await _setAside(householdId, change, error.toString());
          quarantined++;
        }
      }

      await _advance(householdId, response);
    });

    return SyncReport(
      pushed: batch.length,
      settled: settled,
      applied: applied,
      quarantined: quarantined,
      cursorSeq: response.cursorSeq,
      snapshotRequired: response.snapshotRequired,
      hasMore: response.hasMore,
    );
  }

  /// A settled gesture releases the fields it was holding.
  ///
  /// Receiving the answer is what clears `pendingFields` — not sending the
  /// gesture. Until the server has spoken, the value on screen is the user's
  /// own and no incoming state may replace it.
  Future<void> _clearPending(OutboxData entry) async {
    if (entry.entity != 'task') {
      return;
    }

    final row =
        await (db.select(db.tasks)
              ..where((t) => t.householdId.equals(entry.householdId))
              ..where((t) => t.id.equals(entry.rowId)))
            .getSingleOrNull();
    if (row == null) {
      return;
    }

    final touched = (jsonDecode(entry.payload) as Map<String, dynamic>).keys;
    final pending = (jsonDecode(row.pendingFields) as List<dynamic>)
        .cast<String>()
        .where((field) => !touched.contains(field))
        .toList();

    await (db.update(db.tasks)
          ..where((t) => t.householdId.equals(entry.householdId))
          ..where((t) => t.id.equals(entry.rowId)))
        .write(TasksCompanion(pendingFields: Value(jsonEncode(pending))));
  }

  Future<void> _applyChange(String householdId, RowChange change) async {
    switch (change.entity) {
      case 'task':
        await _applyTask(householdId, change);
      case 'member':
        await _applyMember(householdId, change);
      case 'notice':
        await _applyNotice(householdId, change);
      default:
        // An entity this version has never heard of. Set aside, and the cursor
        // still moves: a newer server must not be able to stop an older app.
        throw FormatException('unknown entity ${change.entity}');
    }
  }

  Future<void> _applyTask(String householdId, RowChange change) async {
    final local =
        await (db.select(db.tasks)
              ..where((t) => t.householdId.equals(householdId))
              ..where((t) => t.id.equals(change.rowId)))
            .getSingleOrNull();

    // Idempotent and order-insensitive: an older state is simply not applied,
    // so a page replayed twice changes nothing.
    if (local != null && change.rev <= local.rev) {
      return;
    }

    final pending = local == null
        ? const <String>[]
        : (jsonDecode(local.pendingFields) as List<dynamic>).cast<String>();

    final state = change.state;
    T? keep<T>(String field, T? incoming, T? current) {
      return pending.contains(field) ? current : incoming;
    }

    final companion = TasksCompanion(
      id: Value(change.rowId),
      householdId: Value(householdId),
      rev: Value(change.rev),
      createdRev: Value(local?.createdRev ?? change.rev),
      deletedRev: Value(change.deleted ? change.rev : null),
      fieldRevs: Value(jsonEncode(state['field_revs'] ?? const {})),
      ext: Value(jsonEncode(state['ext'] ?? const {})),
      pendingFields: Value(local?.pendingFields ?? '[]'),
      kind: Value(
        keep('kind', state['kind'] as String?, local?.kind) ?? 'task',
      ),
      title: Value(
        keep('title', state['title'] as String?, local?.title) ?? '',
      ),
      notes: Value(keep('notes', state['notes'] as String?, local?.notes)),
      dueOn: Value(keep('due_on', state['due_on'] as String?, local?.dueOn)),
      dueTime: Value(
        keep('due_time', state['due_time'] as String?, local?.dueTime),
      ),
      allDay: Value(
        keep(
              'all_day',
              (state['all_day'] as bool?) == false ? 0 : 1,
              local?.allDay,
            ) ??
            1,
      ),
      assigneeId: Value(
        keep('assignee_id', state['assignee_id'] as String?, local?.assigneeId),
      ),
      categoryId: Value(
        keep('category_id', state['category_id'] as String?, local?.categoryId),
      ),
      priority: Value(
        keep(
          'priority',
          (state['priority'] as num?)?.toInt(),
          local?.priority,
        ),
      ),
      seriesId: Value(state['series_id'] as String?),
      seriesIndex: Value((state['series_index'] as num?)?.toInt()),
      overrides: Value(jsonEncode(state['overrides'] ?? const [])),
      completions: Value(jsonEncode(state['completions'] ?? const [])),
      tags: Value(jsonEncode(state['tags'] ?? const [])),
      provisional: const Value(0),
    );

    await db.into(db.tasks).insertOnConflictUpdate(companion);
  }

  Future<void> _applyMember(String householdId, RowChange change) async {
    final local =
        await (db.select(db.members)
              ..where((m) => m.householdId.equals(householdId))
              ..where((m) => m.id.equals(change.rowId)))
            .getSingleOrNull();
    if (local != null && change.rev <= local.rev) {
      return;
    }

    await db
        .into(db.members)
        .insertOnConflictUpdate(
          MembersCompanion(
            id: Value(change.rowId),
            householdId: Value(householdId),
            rev: Value(change.rev),
            createdRev: Value(local?.createdRev ?? change.rev),
            deletedRev: Value(change.deleted ? change.rev : null),
            role: Value(change.state['role'] as String? ?? 'adult'),
            removed: Value((change.state['removed'] as bool? ?? false) ? 1 : 0),
            fields: Value(jsonEncode(change.state['fields'] ?? const {})),
            fieldRevs: Value(
              jsonEncode(change.state['field_revs'] ?? const {}),
            ),
          ),
        );
  }

  Future<void> _applyNotice(String householdId, RowChange change) async {
    final local =
        await (db.select(db.notices)
              ..where((n) => n.householdId.equals(householdId))
              ..where((n) => n.id.equals(change.rowId)))
            .getSingleOrNull();
    if (local != null && change.rev <= local.rev) {
      return;
    }

    final state = change.state;
    await db
        .into(db.notices)
        .insertOnConflictUpdate(
          NoticesCompanion(
            id: Value(change.rowId),
            householdId: Value(householdId),
            rev: Value(change.rev),
            createdRev: Value(local?.createdRev ?? change.rev),
            forMember: Value(state['for_member'] as String? ?? ''),
            kind: Value(state['kind'] as String? ?? ''),
            code: Value(state['code'] as String? ?? ''),
            targetEntity: Value(state['entity'] as String?),
            targetRowId: Value(state['row_id'] as String?),
            mine: Value(jsonEncode(state['mine'] ?? const {})),
            theirs: Value(
              state['theirs'] == null ? null : jsonEncode(state['theirs']),
            ),
            byMember: Value(state['by_member'] as String?),
            opId: Value(state['op_id'] as String?),
            resolved: Value((state['resolved'] as bool? ?? false) ? 1 : 0),
            // Local only, and deliberately preserved: the server has no idea
            // whether somebody has looked at the card.
            seen: Value(local?.seen ?? 0),
          ),
        );
  }

  Future<void> _setAside(
    String householdId,
    RowChange change,
    String error,
  ) async {
    await db
        .into(db.quarantine)
        .insert(
          QuarantineCompanion.insert(
            householdId: householdId,
            entity: change.entity,
            rowId: change.rowId,
            rev: change.rev,
            raw: jsonEncode(change.state),
            error: error,
            atElapsed: _elapsed,
          ),
        );
  }

  Future<void> _advance(String householdId, SyncResponse response) async {
    await db
        .into(db.syncCursors)
        .insertOnConflictUpdate(
          SyncCursorsCompanion(
            householdId: Value(householdId),
            cursorSeq: Value(response.cursorSeq),
            purgeHorizonSeq: Value(response.purgeHorizonSeq),
            lastSyncElapsed: Value(_elapsed),
            lastOutcome: const Value('ok'),
          ),
        );
  }
}
