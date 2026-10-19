import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:drift/drift.dart';
import 'package:uuid/uuid.dart';

/// Every write the user makes, and the only path they take.
///
/// Each gesture does two things in ONE transaction: it changes the local row,
/// and it queues the intent. One transaction and not two, because a crash
/// between them would either lose the gesture or show a change that will never
/// be sent — and the second is worse, since the person has no way to tell.
///
/// The local row changes FIRST and immediately. Nobody waits for a server to
/// see what they just typed: that is what "offline first, not offline
/// tolerated" means in practice.
class TaskRepository {
  TaskRepository({
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

  /// Time-ordered, so rows created on a phone sort sensibly even before any
  /// server has seen them.
  static String _uuidV7() => const Uuid().v7();

  static int _defaultElapsed() => DateTime.now().millisecondsSinceEpoch;

  /// Everything the household still has open, newest first, as a live stream.
  ///
  /// A stream straight off the local database is the whole state management
  /// this app needs: the screen watches SQLite, the sync engine writes to
  /// SQLite, and the screen catches up on its own. One fewer moving part to
  /// justify (operating rule 4).
  Stream<List<Task>> watchOpen() {
    return (db.select(db.tasks)
          ..where((t) => t.householdId.equals(householdId))
          ..where((t) => t.deletedRev.isNull())
          ..orderBy([(t) => OrderingTerm.desc(t.rev)]))
        .watch();
  }

  /// Create a task.
  ///
  /// Only the title is required — product principle 1, and the reason ten
  /// seconds is achievable at all.
  Future<String> create(String title) async {
    final id = _newId();

    await db.transaction(() async {
      await db
          .into(db.tasks)
          .insert(
            TasksCompanion.insert(
              id: id,
              householdId: householdId,
              // No revision yet: the server assigns it. Zero sorts this row to
              // the top of a list ordered by rev descending... which is wrong,
              // so creations carry the current head instead.
              rev: await _nextLocalRev(),
              title: Value(title),
              updatedBy: Value(memberId),
              // The title is the user's until the server answers about it.
              pendingFields: const Value('["title"]'),
            ),
          );

      await _enqueue(
        kind: 'create',
        rowId: id,
        payload: {
          'fields': {'title': title},
        },
      );
    });

    return id;
  }

  /// Rename a task, carrying the revision the person actually had in front of
  /// them.
  ///
  /// That revision is what makes conflict detection causal instead of
  /// chronological: "did this author see the current value" is a fact about our
  /// own sequence, and it stays true whatever the phone thinks the time is.
  Future<void> rename(Task task, String title) async {
    final seen = _fieldRev(task, 'title');

    await db.transaction(() async {
      await _patch(
        task,
        TasksCompanion(title: Value(title)),
        pending: {'title'},
      );
      await _enqueue(
        kind: 'set_fields',
        rowId: task.id,
        payload: {
          'fields': {'title': title},
        },
        baseRevs: {'title': seen},
      );
    });
  }

  /// Tick a task.
  ///
  /// Completions are a set indexed by member, so this adds one rather than
  /// setting a flag: two adults ticking the same chore are not in conflict.
  Future<void> complete(Task task) async {
    final completionId = _newId();
    final existing = _completions(task);

    if (existing.any((c) => c['member_id'] == memberId)) {
      // Already ticked by this member. The completion id is the deduplication
      // key for points, so doing it twice must not count twice.
      return;
    }

    final now = _wallClock();

    await db.transaction(() async {
      await _patch(
        task,
        TasksCompanion(
          completions: Value(
            jsonEncode([
              ...existing,
              {
                'member_id': memberId,
                'completion_id': completionId,
                'occurred_on': _dateOnly(now),
                'clock_suspect': false,
              },
            ]),
          ),
        ),
        // Held until the server answers, so another member's state arriving in
        // the meantime cannot make this tick disappear under the person who
        // made it.
        pending: {'completions'},
      );

      await _enqueue(
        kind: 'complete',
        rowId: task.id,
        payload: {
          'completion_id': completionId,
          'occurred_on': _dateOnly(now),
        },
      );
    });
  }

  /// Untick, removing exactly what this person could see.
  ///
  /// Never "all of them": unticking in the kitchen must not erase a completion
  /// recorded in the garage ten seconds earlier.
  Future<void> uncomplete(Task task) async {
    final observed = _completions(
      task,
    ).map((c) => c['member_id'] as String?).whereType<String>().toList();

    if (observed.isEmpty) {
      return;
    }

    await db.transaction(() async {
      await _patch(
        task,
        const TasksCompanion(completions: Value('[]')),
        pending: {'completions'},
      );
      await _enqueue(
        kind: 'uncomplete',
        rowId: task.id,
        payload: {'observed': observed},
      );
    });
  }

  /// Bury a task, keeping its content.
  ///
  /// A tombstone locally too: the row leaves the list at once, but it can still
  /// say what the thing was called — and if the server refuses the deletion
  /// because somebody had edited it, the state that comes back brings it right
  /// out of the grave.
  Future<void> remove(Task task) async {
    await db.transaction(() async {
      await (db.update(db.tasks)
            ..where((t) => t.householdId.equals(householdId))
            ..where((t) => t.id.equals(task.id)))
          .write(
            TasksCompanion(
              deletedRev: Value(task.rev),
              updatedBy: Value(memberId),
            ),
          );

      await _enqueue(
        kind: 'delete',
        rowId: task.id,
        payload: {'base_rev': task.rev},
      );
    });
  }

  // --- the plumbing ---------------------------------------------------------

  Future<void> _patch(
    Task task,
    TasksCompanion change, {
    Set<String> pending = const {},
  }) async {
    final held = {
      ...(jsonDecode(task.pendingFields) as List<dynamic>).cast<String>(),
      ...pending,
    };

    await (db.update(db.tasks)
          ..where((t) => t.householdId.equals(householdId))
          ..where((t) => t.id.equals(task.id)))
        .write(
          change.copyWith(
            updatedBy: Value(memberId),
            pendingFields: Value(jsonEncode(held.toList())),
          ),
        );
  }

  /// Queue one intent.
  ///
  /// [payload] is the operation's own body, and it differs by kind — changed
  /// fields for a rename, a completion id for a tick, the observed members for
  /// an untick. The engine merges it into the wire form, so the queue stores
  /// what the gesture meant rather than a shape bent to fit every kind at once.
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
            entity: 'task',
            rowId: rowId,
            payload: jsonEncode(payload),
            baseRevs: Value(jsonEncode(baseRevs)),
            // The wall clock, for display only. Never for ordering.
            clientTs: Value(_wallClock().millisecondsSinceEpoch),
            // Monotonic ticks, for backoff. Kept apart from the wall clock
            // because a backoff computed from one goes haywire the moment the
            // network corrects the time.
            enqueuedElapsed: _elapsed(),
          ),
        );
  }

  /// A creation carries the household's current position rather than zero, so
  /// it does not jump above rows the server has already numbered.
  Future<int> _nextLocalRev() async {
    final cursor = await db.cursorFor(householdId);
    return cursor?.cursorSeq ?? 0;
  }

  int _fieldRev(Task task, String field) {
    final revs = jsonDecode(task.fieldRevs) as Map<String, dynamic>;
    return (revs[field] as num?)?.toInt() ?? 0;
  }

  List<Map<String, dynamic>> _completions(Task task) {
    return (jsonDecode(task.completions) as List<dynamic>)
        .cast<Map<String, dynamic>>();
  }

  /// A DATE and not an instant: "I did it on Tuesday" must not move because
  /// somebody travelled.
  static String _dateOnly(DateTime moment) {
    final month = moment.month.toString().padLeft(2, '0');
    final day = moment.day.toString().padLeft(2, '0');
    return '${moment.year}-$month-$day';
  }
}
