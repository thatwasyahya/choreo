import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/task_repository.dart';
import 'package:drift/drift.dart' show OrderingTerm, Value;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';

const _household = 'home';
const _me = 'member-me';
const _other = 'member-other';

void main() {
  late AppDatabase db;
  late TaskRepository repo;

  // No network anywhere in this file, and no mock of one either. Every gesture
  // below has to work with nothing but the phone — which is the product, not a
  // test convenience.
  setUp(() {
    db = AppDatabase(NativeDatabase.memory());

    var counter = 0;
    repo = TaskRepository(
      db: db,
      householdId: _household,
      memberId: _me,
      newId: () => 'id-${counter++}',
      elapsed: () => 0,
      wallClock: () => DateTime.utc(2026, 10, 19, 14, 30),
    );
  });
  tearDown(() => db.close());

  Future<List<OutboxData>> queued() {
    return (db.select(
      db.outbox,
    )..orderBy([(o) => OrderingTerm.asc(o.lseq)])).get();
  }

  Future<Task> reread(String id) {
    return (db.select(db.tasks)..where((t) => t.id.equals(id))).getSingle();
  }

  List<String> pendingOf(Task task) {
    return (jsonDecode(task.pendingFields) as List<dynamic>).cast<String>();
  }

  Map<String, dynamic> payloadOf(OutboxData entry) {
    return jsonDecode(entry.payload) as Map<String, dynamic>;
  }

  // --- creating --------------------------------------------------------------

  test('a new task is visible at once and queued once', () async {
    final id = await repo.create('Sortir les poubelles');

    final stored = await reread(id);
    expect(stored.title, 'Sortir les poubelles');

    final ops = await queued();
    expect(ops, hasLength(1));
    expect(ops.single.kind, 'create');
    expect(ops.single.rowId, id);
    expect(payloadOf(ops.single)['fields'], {'title': 'Sortir les poubelles'});
  });

  // Product principle 1, and the reason ten seconds is achievable at all.
  test('only the title is needed', () async {
    final id = await repo.create('Lait');

    final stored = await reread(id);
    expect(stored.dueOn, isNull);
    expect(stored.assigneeId, isNull);
    expect(stored.notes, isNull);
  });

  test('the screen sees each gesture without being told', () async {
    final seen = <int>[];
    final subscription = repo.watchOpen().listen((tasks) {
      seen.add(tasks.length);
    });

    await repo.create('Première');
    await repo.create('Deuxième');
    await pumpEventQueue();

    await subscription.cancel();
    expect(seen.last, 2);
  });

  // --- renaming --------------------------------------------------------------

  // The revision the person actually had in front of them is what makes
  // conflict detection causal rather than chronological.
  test('a rename carries the revision the person could see', () async {
    final id = await repo.create('Ancien titre');

    // The server has since answered about this row.
    await (db.update(db.tasks)..where((t) => t.id.equals(id))).write(
      const TasksCompanion(
        rev: Value(48102),
        fieldRevs: Value('{"title": 48102}'),
        pendingFields: Value('[]'),
      ),
    );

    await repo.rename(await reread(id), 'Nouveau titre');

    final rename = (await queued()).last;
    expect(rename.kind, 'set_fields');
    expect(jsonDecode(rename.baseRevs), {'title': 48102});
  });

  test('a rename holds the field until the server answers', () async {
    final id = await repo.create('Titre');
    await (db.update(db.tasks)..where((t) => t.id.equals(id))).write(
      const TasksCompanion(pendingFields: Value('[]')),
    );

    await repo.rename(await reread(id), 'Mon titre');

    expect(pendingOf(await reread(id)), contains('title'));
  });

  // --- ticking ---------------------------------------------------------------

  test('ticking records a completion for this member', () async {
    final id = await repo.create('Poubelles');

    await repo.complete(await reread(id));

    final stored = await reread(id);
    final completions = (jsonDecode(stored.completions) as List<dynamic>)
        .cast<Map<String, dynamic>>();
    expect(completions, hasLength(1));
    expect(completions.single['member_id'], _me);
    expect(
      completions.single['occurred_on'],
      '2026-10-19',
      reason: 'a completion is dated by DAY, never by instant',
    );

    final tick = (await queued()).last;
    expect(tick.kind, 'complete');
    expect(payloadOf(tick)['completion_id'], isNotNull);
  });

  // The completion id is the deduplication key for points, so the same person
  // ticking twice must not count twice.
  test('ticking twice as the same member queues nothing more', () async {
    final id = await repo.create('Poubelles');

    await repo.complete(await reread(id));
    final afterFirst = (await queued()).length;
    await repo.complete(await reread(id));

    expect((await queued()).length, afterFirst);
  });

  // Another member's state must not be able to make somebody's own tick vanish
  // under them before the server has answered.
  test('ticking holds the completion set', () async {
    final id = await repo.create('Poubelles');
    await repo.complete(await reread(id));

    expect(pendingOf(await reread(id)), contains('completions'));
  });

  // Unticking removes exactly what this person could see: unticking in the
  // kitchen must not erase a completion recorded in the garage.
  test('unticking carries the members it observed', () async {
    final id = await repo.create('Poubelles');

    await (db.update(db.tasks)..where((t) => t.id.equals(id))).write(
      TasksCompanion(
        completions: Value(
          jsonEncode([
            {'member_id': _me, 'completion_id': 'c-1'},
            {'member_id': _other, 'completion_id': 'c-2'},
          ]),
        ),
      ),
    );

    await repo.uncomplete(await reread(id));

    final untick = (await queued()).last;
    expect(untick.kind, 'uncomplete');
    expect(payloadOf(untick)['observed'], containsAll([_me, _other]));
  });

  test('unticking nothing queues nothing', () async {
    final id = await repo.create('Poubelles');
    final before = (await queued()).length;

    await repo.uncomplete(await reread(id));

    expect((await queued()).length, before);
  });

  // --- removing --------------------------------------------------------------

  test('removing hides the row at once and keeps its content', () async {
    final id = await repo.create('Acheter du lait');

    await repo.remove(await reread(id));

    expect(await db.countTasks(_household), 0);
    final buried = await reread(id);
    expect(
      buried.title,
      'Acheter du lait',
      reason:
          'a tombstone keeps the content, so the row can still say what it was',
    );

    final delete = (await queued()).last;
    expect(delete.kind, 'delete');
    expect(
      payloadOf(delete)['base_rev'],
      isNotNull,
      reason: 'a deletion asks against a revision, it does not impose',
    );
  });

  // --- the queue -------------------------------------------------------------

  // Replay order comes from an autoincrement. The wall clock is frozen in these
  // tests, which would make a timestamp-ordered queue indistinguishable from a
  // shuffled one.
  test('gestures queue in the order they were made', () async {
    final first = await repo.create('Première');
    await repo.rename(await reread(first), 'Première, renommée');
    await repo.complete(await reread(first));

    final ops = await queued();
    expect(ops.map((o) => o.kind), ['create', 'set_fields', 'complete']);
  });

  // One transaction per gesture: a crash between the local change and the queue
  // entry would either lose the gesture or show a change that will never be
  // sent — and the second is worse, because nobody can tell.
  test('a gesture never leaves half of itself behind', () async {
    final id = await repo.create('Poubelles');

    final ops = await queued();
    final stored = await reread(id);

    expect(ops, hasLength(1));
    expect(stored.title, 'Poubelles');
    expect(ops.single.rowId, stored.id);
  });
}
