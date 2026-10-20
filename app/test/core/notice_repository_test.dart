import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/notice_repository.dart';
import 'package:drift/drift.dart' show OrderingTerm, Value;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';

const _household = 'home';
const _me = 'member-me';
const _other = 'member-other';

void main() {
  late AppDatabase db;
  late NoticeRepository repo;

  setUp(() {
    db = AppDatabase(NativeDatabase.memory());

    var counter = 0;
    repo = NoticeRepository(
      db: db,
      householdId: _household,
      memberId: _me,
      newId: () => 'id-${counter++}',
      elapsed: () => 0,
      wallClock: () => DateTime.utc(2026, 10, 19, 14, 30),
    );
  });
  tearDown(() => db.close());

  Future<void> addNotice(
    String id, {
    String forMember = _me,
    String householdId = _household,
    String code = 'field_superseded',
    String? targetEntity = 'task',
    String? targetRowId = 'task-1',
    Map<String, dynamic> mine = const {'title': 'Mon titre'},
    Map<String, dynamic>? theirs = const {'title': 'Leur titre'},
    int rev = 10,
    int resolved = 0,
  }) {
    return db
        .into(db.notices)
        .insert(
          NoticesCompanion.insert(
            id: id,
            householdId: householdId,
            rev: rev,
            forMember: Value(forMember),
            kind: const Value('receipt'),
            code: Value(code),
            targetEntity: Value(targetEntity),
            targetRowId: Value(targetRowId),
            mine: Value(jsonEncode(mine)),
            theirs: Value(theirs == null ? null : jsonEncode(theirs)),
            resolved: Value(resolved),
          ),
        );
  }

  Future<void> addTask({
    String id = 'task-1',
    String title = 'Leur titre',
    int rev = 9,
    Map<String, int> fieldRevs = const {'title': 9},
  }) {
    return db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: id,
            householdId: _household,
            rev: rev,
            title: Value(title),
            fieldRevs: Value(jsonEncode(fieldRevs)),
          ),
        );
  }

  Future<List<OutboxData>> queued() {
    return (db.select(
      db.outbox,
    )..orderBy([(o) => OrderingTerm.asc(o.lseq)])).get();
  }

  Map<String, dynamic> payloadOf(OutboxData entry) {
    return jsonDecode(entry.payload) as Map<String, dynamic>;
  }

  Future<Notice> notice(String id) {
    return (db.select(db.notices)..where((n) => n.id.equals(id))).getSingle();
  }

  Future<Task> task(String id) {
    return (db.select(db.tasks)..where((t) => t.id.equals(id))).getSingle();
  }

  // --- the tray --------------------------------------------------------------

  test('the tray holds the open cards, oldest first', () async {
    await addNotice('late', rev: 20);
    await addNotice('early', rev: 11);

    final cards = await repo.watchOpen().first;

    expect(
      cards.map((n) => n.id),
      ['early', 'late'],
      reason: 'a tray is a queue: what has waited longest is settled next',
    );
  });

  test('an answered card leaves the tray', () async {
    await addNotice('answered', resolved: 1);

    expect(await repo.watchOpen().first, isEmpty);
  });

  // A decision is addressed to one person. Showing somebody else's card would
  // both leak it and invite two voices to answer it.
  test("somebody else's card is not in the tray", () async {
    await addNotice('theirs', forMember: _other);

    expect(await repo.watchOpen().first, isEmpty);
  });

  test("another household's card is not in the tray", () async {
    await addNotice('elsewhere', householdId: 'somebody-else');

    expect(await repo.watchOpen().first, isEmpty);
  });

  // --- answering -------------------------------------------------------------

  test('answering closes the card at once and queues the answer', () async {
    await addTask();
    await addNotice('notice-1');

    await repo.resolve(await notice('notice-1'), NoticeChoice.theirs);

    expect(
      (await notice('notice-1')).resolved,
      1,
      reason: 'nobody should watch a decision they have already made',
    );

    final answer = (await queued()).single;
    expect(answer.kind, 'notice_resolve');
    expect(answer.entity, 'notice');
    expect(answer.rowId, 'notice-1');
    expect(payloadOf(answer)['fields'], {'choice': 'theirs'});
  });

  test('closing a card without choosing is still an answer', () async {
    await addTask();
    await addNotice('notice-1');

    await repo.resolve(await notice('notice-1'), NoticeChoice.dismiss);

    expect(payloadOf((await queued()).single)['fields'], {
      'choice': 'dismiss',
    });
  });

  // --- putting the text back -------------------------------------------------

  // The notice carries the author's text, so this replays it as a fresh write
  // against the current revision — arbitrated like any other write rather than
  // applied by privilege.
  test('put mine back rewrites against what is there now', () async {
    await addTask(fieldRevs: {'title': 48102});
    await addNotice('notice-1', mine: {'title': 'Mon titre'});

    await repo.resolve(await notice('notice-1'), NoticeChoice.mine);

    final ops = await queued();
    expect(ops.map((o) => o.kind), ['set_fields', 'notice_resolve']);

    final rewrite = ops.first;
    expect(rewrite.rowId, 'task-1');
    expect(payloadOf(rewrite)['fields'], {'title': 'Mon titre'});
    expect(
      jsonDecode(rewrite.baseRevs),
      {'title': 48102},
      reason: 'the rewrite asks against the revision it can see, not against 0',
    );
  });

  test('put mine back shows the text immediately and holds it', () async {
    await addTask();
    await addNotice('notice-1', mine: {'title': 'Mon titre'});

    await repo.resolve(await notice('notice-1'), NoticeChoice.mine);

    final restored = await task('task-1');
    expect(restored.title, 'Mon titre');
    expect(
      (jsonDecode(restored.pendingFields) as List<dynamic>).cast<String>(),
      contains('title'),
      reason: 'held, or the next incoming state revokes it under the person',
    );
  });

  // Forward compatibility, from the other side: a newer app may have written a
  // field this version has never heard of. The rescue still has to be whole.
  test('a field this version cannot show still travels', () async {
    await addTask();
    await addNotice(
      'notice-1',
      mine: {'title': 'Mon titre', 'mood': 'pressé'},
    );

    await repo.resolve(await notice('notice-1'), NoticeChoice.mine);

    expect(payloadOf((await queued()).first)['fields'], {
      'title': 'Mon titre',
      'mood': 'pressé',
    });
  });

  test('keeping theirs rewrites nothing', () async {
    await addTask();
    await addNotice('notice-1');

    await repo.resolve(await notice('notice-1'), NoticeChoice.theirs);

    expect((await queued()).map((o) => o.kind), ['notice_resolve']);
    expect((await task('task-1')).title, 'Leur titre');
  });

  // A deletion that really happened is not a reason to leave somebody holding a
  // card they cannot answer.
  test('a card whose target has gone still closes', () async {
    await addNotice('notice-1', targetRowId: 'task-gone');

    await repo.resolve(await notice('notice-1'), NoticeChoice.mine);

    expect((await queued()).map((o) => o.kind), ['notice_resolve']);
    expect((await notice('notice-1')).resolved, 1);
  });

  test('a card with no text of its own closes without rewriting', () async {
    await addTask();
    await addNotice('notice-1', code: 'target_gone', mine: const {});

    await repo.resolve(await notice('notice-1'), NoticeChoice.mine);

    expect((await queued()).map((o) => o.kind), ['notice_resolve']);
  });

  // One transaction for the answer: a crash between the rewrite and the closing
  // would either lose the text the person just rescued, or close a card whose
  // rescue never happened.
  test('an answer never leaves half of itself behind', () async {
    await addTask();
    await addNotice('notice-1');

    await repo.resolve(await notice('notice-1'), NoticeChoice.mine);

    expect(await queued(), hasLength(2));
    expect((await notice('notice-1')).resolved, 1);
    expect((await task('task-1')).title, 'Mon titre');
  });
}
