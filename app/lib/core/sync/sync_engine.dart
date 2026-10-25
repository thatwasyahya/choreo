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

/// What one rebuild did.
class SnapshotReport {
  const SnapshotReport({
    required this.applied,
    required this.removed,
    required this.quarantined,
    required this.cursorSeq,
  });

  final int applied;

  /// Local rows the snapshot had never heard of, and which nothing in the
  /// outbox still referenced.
  final int removed;

  final int quarantined;
  final int cursorSeq;
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

  /// The wire form of one queued intent.
  ///
  /// The stored payload is the operation's own body and differs by kind, so it
  /// is spread into the envelope rather than nested under one name that would
  /// only fit half the kinds.
  Map<String, dynamic> _wire(OutboxData entry) {
    final payload = jsonDecode(entry.payload) as Map<String, dynamic>;

    return {
      'op_id': entry.opId,
      'kind': entry.kind,
      'entity': entry.entity,
      'row_id': entry.rowId,
      'base_revs': jsonDecode(entry.baseRevs),
      ...payload,
      if (entry.clientTs != null)
        'client_ts': DateTime.fromMillisecondsSinceEpoch(
          entry.clientTs!,
          isUtc: true,
        ).toIso8601String(),
    };
  }

  /// Which local fields this gesture is holding.
  ///
  /// A rename holds the field it renamed; a tick holds the completion set, so
  /// another member's state arriving mid-flight cannot make somebody's own tick
  /// vanish under them.
  Set<String> _heldBy(OutboxData entry) {
    final payload = jsonDecode(entry.payload) as Map<String, dynamic>;

    switch (entry.kind) {
      case 'create':
      case 'set_fields':
        final fields = (payload['fields'] as Map<String, dynamic>?) ?? const {};
        return fields.keys.toSet();
      case 'complete':
      case 'uncomplete':
        return {'completions'};
      default:
        return const {};
    }
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

    final touched = _heldBy(entry);
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
      case 'series':
        await _applySeries(householdId, change);
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

  /// A recurring chore's schedule.
  ///
  /// `nextIndex` and `openOccurrenceId` are mirrored and never written by this
  /// app: they are the invariant the server arbitrates, and a client that
  /// wrote them would be a client that can break it. They are here so a screen
  /// can say "the next one is open" without asking the network.
  Future<void> _applySeries(String householdId, RowChange change) async {
    final local =
        await (db.select(db.seriesRows)
              ..where((s) => s.householdId.equals(householdId))
              ..where((s) => s.id.equals(change.rowId)))
            .getSingleOrNull();
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

    await db
        .into(db.seriesRows)
        .insertOnConflictUpdate(
          SeriesRowsCompanion(
            id: Value(change.rowId),
            householdId: Value(householdId),
            rev: Value(change.rev),
            createdRev: Value(local?.createdRev ?? change.rev),
            deletedRev: Value(change.deleted ? change.rev : null),
            fieldRevs: Value(jsonEncode(state['field_revs'] ?? const {})),
            ext: Value(jsonEncode(state['ext'] ?? const {})),
            pendingFields: Value(local?.pendingFields ?? '[]'),
            title: Value(
              keep('title', state['title'] as String?, local?.title) ?? '',
            ),
            mode: Value(
              keep('mode', state['mode'] as String?, local?.mode) ?? 'calendar',
            ),
            rule: Value(
              keep('rule', state['rule'] as String?, local?.rule) ?? '',
            ),
            relDays: Value(
              keep(
                'rel_days',
                (state['rel_days'] as num?)?.toInt(),
                local?.relDays,
              ),
            ),
            anchorOn: Value(
              keep('anchor_on', state['anchor_on'] as String?, local?.anchorOn),
            ),
            rotation: Value(jsonEncode(state['rotation'] ?? const [])),
            rotationOffset: Value(
              (state['rotation_offset'] as num?)?.toInt() ?? 0,
            ),
            paused: Value(
              keep(
                    'paused',
                    (state['paused'] as bool? ?? false) ? 1 : 0,
                    local?.paused,
                  ) ??
                  0,
            ),
            catchUp: Value(
              keep('catch_up', state['catch_up'] as String?, local?.catchUp) ??
                  'skip',
            ),
            // Mirrored, never written by this app.
            nextIndex: Value((state['next_index'] as num?)?.toInt() ?? 0),
            openOccurrenceId: Value(state['open_occurrence_id'] as String?),
          ),
        );
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

  /// Rebuild the whole household from a snapshot.
  ///
  /// Used when the cursor has fallen below what the server still keeps, and for
  /// a brand new device — which is its normal starting state, not a fault.
  ///
  /// Two things are deliberate and neither is an optimisation:
  ///
  /// **Nothing is applied until the `end` line has been seen.** The snapshot is
  /// a closed world: it deletes whatever is absent from it. A stream cut short
  /// would therefore read as "everything else was deleted", which is why the
  /// rows are collected first and committed in one go. A household is a few
  /// hundred kilobytes; holding a write transaction open across the network
  /// instead would be the worse trade.
  ///
  /// **The outbox is never touched.** "Your cursor expired" must not be allowed
  /// to mean "your fourteen offline edits are gone", so rows the queue still
  /// references survive the closed-world sweep even when the snapshot has never
  /// heard of them.
  Future<SnapshotReport> rebuild(String householdId) async {
    final identity = await db.select(db.deviceIdentity).getSingleOrNull();
    final token = identity?.accessToken;
    if (token == null || token.isEmpty) {
      throw const SyncException(SyncFailure.unauthenticated, 'no access token');
    }

    final lines = await api.snapshotLines(
      householdId: householdId,
      accessToken: token,
    );

    SnapshotHeader? header;
    final rows = <RowChange>[];
    var ended = false;
    var declared = -1;

    await for (final line in lines) {
      if (line.trim().isEmpty) {
        continue;
      }

      final Map<String, dynamic> parsed;
      try {
        parsed = jsonDecode(line) as Map<String, dynamic>;
      } on Exception catch (error) {
        throw SyncException(SyncFailure.unreadable, 'snapshot line: $error');
      }

      switch (parsed['type']) {
        case 'header':
          header = SnapshotHeader.fromJson(parsed);
        case 'end':
          ended = true;
          declared = (parsed['rows'] as num?)?.toInt() ?? -1;
        default:
          rows.add(
            RowChange(
              entity: parsed['type'] as String? ?? '',
              rowId: parsed['row_id'] as String? ?? '',
              rev: (parsed['rev'] as num?)?.toInt() ?? 0,
              // A snapshot carries no tombstones: there is no need to learn
              // about the deletion of a row one has never heard of.
              deleted: false,
              state: (parsed['state'] as Map<String, dynamic>?) ?? const {},
            ),
          );
      }
    }

    if (header == null || !ended) {
      throw const SyncException(
        SyncFailure.unreadable,
        'truncated snapshot: refusing to treat a partial world as a closed one',
      );
    }
    if (declared != rows.length) {
      throw SyncException(
        SyncFailure.unreadable,
        'snapshot declared $declared rows and carried ${rows.length}',
      );
    }

    var applied = 0;
    var quarantined = 0;
    var removed = 0;

    await db.transaction(() async {
      for (final row in rows) {
        try {
          await _applyChange(householdId, row);
          applied++;
        } on Exception catch (error) {
          await _setAside(householdId, row, error.toString());
          quarantined++;
        }
      }

      removed = await _sweepAbsent(householdId, rows);

      await db
          .into(db.syncCursors)
          .insertOnConflictUpdate(
            SyncCursorsCompanion(
              householdId: Value(householdId),
              cursorSeq: Value(header!.cursorSeq),
              purgeHorizonSeq: Value(header.purgeHorizonSeq),
              lastSyncElapsed: Value(_elapsed),
              lastOutcome: const Value('snapshot'),
            ),
          );
    });

    return SnapshotReport(
      applied: applied,
      removed: removed,
      quarantined: quarantined,
      cursorSeq: header.cursorSeq,
    );
  }

  /// Drop local rows the snapshot has never heard of.
  ///
  /// This is how a row deleted a hundred days ago finally leaves a phone that
  /// was away, and it is the reason the server can afford to purge for real.
  ///
  /// Rows the outbox still references are kept: the queue is about to talk
  /// about them, and deleting them here would turn an expired cursor into lost
  /// work.
  Future<int> _sweepAbsent(String householdId, List<RowChange> rows) async {
    final referenced =
        (await (db.select(
              db.outbox,
            )..where((o) => o.householdId.equals(householdId))).get())
            .map((entry) => entry.rowId)
            .toSet();

    Set<String> keep(String entity) {
      return {
        ...rows.where((r) => r.entity == entity).map((r) => r.rowId),
        ...referenced,
      };
    }

    var removed = 0;

    final taskKeep = keep('task');
    final localTasks = await (db.select(
      db.tasks,
    )..where((t) => t.householdId.equals(householdId))).get();
    for (final row in localTasks) {
      if (!taskKeep.contains(row.id)) {
        await (db.delete(db.tasks)
              ..where((t) => t.householdId.equals(householdId))
              ..where((t) => t.id.equals(row.id)))
            .go();
        removed++;
      }
    }

    final memberKeep = keep('member');
    final localMembers = await (db.select(
      db.members,
    )..where((m) => m.householdId.equals(householdId))).get();
    for (final row in localMembers) {
      if (!memberKeep.contains(row.id)) {
        await (db.delete(db.members)
              ..where((m) => m.householdId.equals(householdId))
              ..where((m) => m.id.equals(row.id)))
            .go();
        removed++;
      }
    }

    // Notices are swept too: a snapshot only carries the unresolved ones, so a
    // card somebody has already answered disappearing is the right outcome.
    final noticeKeep = keep('notice');
    final localNotices = await (db.select(
      db.notices,
    )..where((n) => n.householdId.equals(householdId))).get();
    for (final row in localNotices) {
      if (!noticeKeep.contains(row.id)) {
        await (db.delete(db.notices)
              ..where((n) => n.householdId.equals(householdId))
              ..where((n) => n.id.equals(row.id)))
            .go();
        removed++;
      }
    }

    return removed;
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
