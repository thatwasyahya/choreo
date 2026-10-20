import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:drift/drift.dart';
import 'package:uuid/uuid.dart';

/// What a person can answer to a card in the review tray.
enum NoticeChoice {
  /// Put my text back. The notice already carries it verbatim, so this replays
  /// it as a fresh write against the current revision — arbitrated like any
  /// other write, rather than smuggled in with a privilege.
  mine('mine'),

  /// Keep what the other person wrote.
  theirs('theirs'),

  /// Neither. Closing the card is itself an answer, and it is recorded.
  dismiss('dismiss');

  const NoticeChoice(this.wire);

  final String wire;
}

/// The review tray: what the server could not do the way somebody asked.
///
/// A notice is never an error dialog. It is a card, it waits, and it carries
/// the author's own text so that "put mine back" is a real offer and not an
/// apology. Answering one works offline like every other gesture — queued in
/// the outbox, not posted to an endpoint.
class NoticeRepository {
  NoticeRepository({
    required this.db,
    required this.householdId,
    required this.memberId,
    String Function()? newId,
    int Function()? elapsed,
    DateTime Function()? wallClock,
  }) : _newId = newId ?? _uuidV7,
       _elapsed = elapsed ?? _defaultElapsed,
       _wallClock = wallClock ?? DateTime.now;

  final AppDatabase db;
  final String householdId;
  final String memberId;

  final String Function() _newId;
  final int Function() _elapsed;
  final DateTime Function() _wallClock;

  static String _uuidV7() => const Uuid().v7();

  static int _defaultElapsed() => DateTime.now().millisecondsSinceEpoch;

  /// The cards addressed to this member and still open, oldest first.
  ///
  /// Oldest first, unlike the task list: a tray is a queue of things to settle,
  /// and the one that has waited longest is the one to settle next.
  ///
  /// `for_member` is part of the query and not a check afterwards. A decision
  /// is addressed to one person, and showing somebody else's card would both
  /// leak it and invite two voices to answer it.
  Stream<List<Notice>> watchOpen() {
    return (db.select(db.notices)
          ..where((n) => n.householdId.equals(householdId))
          ..where((n) => n.forMember.equals(memberId))
          ..where((n) => n.resolved.equals(0))
          ..where((n) => n.deletedRev.isNull())
          ..orderBy([(n) => OrderingTerm.asc(n.rev)]))
        .watch();
  }

  /// How many cards are waiting, as a live count for the badge.
  Stream<int> watchOpenCount() {
    return watchOpen().map((notices) => notices.length);
  }

  /// Answer a card.
  ///
  /// The card closes locally at once — nobody should have to watch a decision
  /// they have already made — and the answer goes up as an ordinary operation.
  /// Choosing [NoticeChoice.mine] also queues the rewrite, in the SAME
  /// transaction: a crash between the two would either lose the text the person
  /// just rescued or close a card whose rescue never happened.
  Future<void> resolve(Notice notice, NoticeChoice choice) async {
    await db.transaction(() async {
      if (choice == NoticeChoice.mine) {
        await _replay(notice);
      }

      await (db.update(db.notices)
            ..where((n) => n.householdId.equals(householdId))
            ..where((n) => n.id.equals(notice.id)))
          .write(const NoticesCompanion(resolved: Value(1), seen: Value(1)));

      await _enqueue(
        kind: 'notice_resolve',
        rowId: notice.id,
        payload: {
          'fields': {'choice': choice.wire},
        },
      );
    });
  }

  /// Put the author's text back, as a fresh write against what is there now.
  ///
  /// If the target row has gone in the meantime there is nothing to write to,
  /// and the card still closes. A deletion that happened is not a reason to
  /// leave somebody with a card they cannot answer.
  Future<void> _replay(Notice notice) async {
    // A flat field → value map: the author's own text, as they wrote it.
    final fields = jsonDecode(notice.mine) as Map<String, dynamic>;

    if (fields.isEmpty ||
        notice.targetEntity != 'task' ||
        notice.targetRowId == null) {
      return;
    }

    final target =
        await (db.select(db.tasks)
              ..where((t) => t.householdId.equals(householdId))
              ..where((t) => t.id.equals(notice.targetRowId!)))
            .getSingleOrNull();
    if (target == null) {
      return;
    }

    final revs = jsonDecode(target.fieldRevs) as Map<String, dynamic>;
    final baseRevs = <String, int>{
      for (final field in fields.keys)
        field: (revs[field] as num?)?.toInt() ?? 0,
    };

    await _patchLocally(target, fields);

    await _enqueue(
      kind: 'set_fields',
      rowId: target.id,
      payload: {'fields': fields},
      baseRevs: baseRevs,
    );
  }

  /// Write back the fields this version knows how to display.
  ///
  /// Only those: a card can carry a field a newer app wrote and this one has
  /// never heard of. It still goes up to the server — which does know it — so
  /// the rescue is complete even where the screen cannot show it.
  Future<void> _patchLocally(Task target, Map<String, dynamic> fields) async {
    final held = {
      ...(jsonDecode(target.pendingFields) as List<dynamic>).cast<String>(),
      ...fields.keys,
    };

    await (db.update(db.tasks)
          ..where((t) => t.householdId.equals(householdId))
          ..where((t) => t.id.equals(target.id)))
        .write(
          TasksCompanion(
            title: fields.containsKey('title')
                ? Value(fields['title'] as String? ?? '')
                : const Value.absent(),
            notes: fields.containsKey('notes')
                ? Value(fields['notes'] as String?)
                : const Value.absent(),
            dueOn: fields.containsKey('due_on')
                ? Value(fields['due_on'] as String?)
                : const Value.absent(),
            updatedBy: Value(memberId),
            pendingFields: Value(jsonEncode(held.toList())),
          ),
        );
  }

  Future<void> _enqueue({
    required String kind,
    required String rowId,
    required Map<String, dynamic> payload,
    Map<String, int> baseRevs = const {},
  }) async {
    await db
        .into(db.outbox)
        .insert(
          OutboxCompanion.insert(
            householdId: householdId,
            opId: _newId(),
            kind: kind,
            entity: kind == 'notice_resolve' ? 'notice' : 'task',
            rowId: rowId,
            payload: jsonEncode(payload),
            baseRevs: Value(jsonEncode(baseRevs)),
            clientTs: Value(_wallClock().millisecondsSinceEpoch),
            enqueuedElapsed: _elapsed(),
          ),
        );
  }
}
