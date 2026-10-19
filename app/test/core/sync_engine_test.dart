import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/sync/sync_api.dart';
import 'package:choreo/core/sync/sync_engine.dart';
import 'package:drift/drift.dart' show Value;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

const _household = 'home';

void main() {
  late AppDatabase db;

  setUp(() async {
    db = AppDatabase(NativeDatabase.memory());
    await db
        .into(db.deviceIdentity)
        .insert(
          DeviceIdentityCompanion.insert(
            deviceId: const Value('device-1'),
            accessToken: const Value('token'),
          ),
        );
  });
  tearDown(() => db.close());

  /// An engine whose server answers with exactly this page.
  SyncEngine engineAnswering(Map<String, dynamic> response) {
    return SyncEngine(
      db: db,
      api: SyncApi(
        httpClient: MockClient(
          (_) async => http.Response(
            jsonEncode(response),
            200,
            headers: {'content-type': 'application/json'},
          ),
        ),
        baseUrl: 'http://example.invalid',
      ),
      now: () => 0,
    );
  }

  Map<String, dynamic> page({
    List<Map<String, dynamic>> results = const [],
    List<Map<String, dynamic>> changes = const [],
    int cursorSeq = 0,
    bool snapshotRequired = false,
  }) {
    return {
      'head_seq': cursorSeq,
      'cursor_seq': cursorSeq,
      'results': results,
      'changes': changes,
      'has_more': false,
      'purge_horizon_seq': 0,
      'snapshot_required': snapshotRequired,
    };
  }

  Map<String, dynamic> taskChange({
    required String rowId,
    required int rev,
    String title = 'Poubelles',
    String? notes,
    bool deleted = false,
  }) {
    return {
      'entity': 'task',
      'row_id': rowId,
      'rev': rev,
      'deleted': deleted,
      'state': {
        'kind': 'task',
        'title': title,
        'notes': notes,
        'all_day': true,
        'field_revs': {'title': rev},
        'ext': <String, dynamic>{},
        'completions': <dynamic>[],
        'tags': <dynamic>[],
      },
    };
  }

  Future<void> queue({
    required String opId,
    required String rowId,
    required Map<String, dynamic> fields,
  }) {
    return db
        .into(db.outbox)
        .insert(
          OutboxCompanion.insert(
            householdId: _household,
            opId: opId,
            kind: 'set_fields',
            entity: 'task',
            rowId: rowId,
            // The payload is the operation's own body: a rename carries its
            // changed fields under 'fields'.
            payload: jsonEncode({'fields': fields}),
            enqueuedElapsed: 0,
          ),
        );
  }

  Future<Task?> task(String rowId) {
    return (db.select(
      db.tasks,
    )..where((t) => t.id.equals(rowId))).getSingleOrNull();
  }

  // --- the rule that matters -------------------------------------------------

  // Synchronisation must never revoke the sentence somebody is in the middle of
  // typing. The field is listed in pendingFields until the server has answered
  // about it, and an incoming state writes everything except those.
  test('an incoming state does not overwrite a field being edited', () async {
    await db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'task-1',
            householdId: _household,
            rev: 10,
            title: const Value('Ce que je tape'),
            notes: const Value('ancienne note'),
            pendingFields: const Value('["title"]'),
          ),
        );

    await engineAnswering(
      page(
        cursorSeq: 20,
        changes: [
          taskChange(
            rowId: 'task-1',
            rev: 20,
            title: 'Ce que le serveur dit',
            notes: 'note du serveur',
          ),
        ],
      ),
    ).syncOnce(_household);

    final stored = await task('task-1');
    expect(
      stored?.title,
      'Ce que je tape',
      reason: 'the field being edited was overwritten under the user',
    );
    // Everything NOT pending is applied, so the row is not frozen either.
    expect(stored?.notes, 'note du serveur');
    expect(stored?.rev, 20);
  });

  // And the answer is what releases the field — not the sending.
  test('a settled gesture releases the field it was holding', () async {
    await db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'task-1',
            householdId: _household,
            rev: 10,
            title: const Value('Mon titre'),
            pendingFields: const Value('["title"]'),
          ),
        );
    await queue(opId: 'op-1', rowId: 'task-1', fields: {'title': 'Mon titre'});

    await engineAnswering(
      page(
        cursorSeq: 11,
        results: [
          {'op_id': 'op-1', 'outcome': 'applied', 'rev': 11},
        ],
      ),
    ).syncOnce(_household);

    final stored = await task('task-1');
    expect(jsonDecode(stored!.pendingFields), isEmpty);
    expect(
      await db.pendingCount(),
      0,
      reason: 'the outbox entry should be gone',
    );
  });

  // --- the outbox ------------------------------------------------------------

  // unsupported_op means the server is older than this app. A deploy fixes it,
  // so dropping the gesture would poison a real person's queue over an
  // ordering mistake.
  test('a retryable refusal stays in the queue', () async {
    await queue(opId: 'op-1', rowId: 'task-1', fields: {'title': 'x'});

    await engineAnswering(
      page(
        cursorSeq: 5,
        results: [
          {
            'op_id': 'op-1',
            'outcome': 'refused',
            'code': 'unsupported_op',
          },
        ],
      ),
    ).syncOnce(_household);

    expect(await db.pendingCount(), 1);
  });

  test('a terminal refusal leaves the queue', () async {
    await queue(opId: 'op-1', rowId: 'task-1', fields: {'title': 'x'});

    await engineAnswering(
      page(
        cursorSeq: 5,
        results: [
          {'op_id': 'op-1', 'outcome': 'refused', 'code': 'target_gone'},
        ],
      ),
    ).syncOnce(_household);

    expect(
      await db.pendingCount(),
      0,
      reason: 'a gesture no retry can ever help must not sit there forever',
    );
  });

  // --- applying a page -------------------------------------------------------

  test('the cursor advances to what was applied', () async {
    final report = await engineAnswering(
      page(
        cursorSeq: 48318,
        changes: [taskChange(rowId: 'task-1', rev: 48314)],
      ),
    ).syncOnce(_household);

    expect(report.applied, 1);
    expect((await db.cursorFor(_household))?.cursorSeq, 48318);
  });

  // Applying a page twice must change nothing: that is what makes the client
  // need no merge engine at all.
  test('replaying a page changes nothing', () async {
    final answer = page(
      cursorSeq: 20,
      changes: [taskChange(rowId: 'task-1', rev: 20)],
    );

    await engineAnswering(answer).syncOnce(_household);
    await engineAnswering(answer).syncOnce(_household);

    expect(await db.countTasks(_household), 1);
    expect((await task('task-1'))?.rev, 20);
  });

  test('an older state is not applied', () async {
    await engineAnswering(
      page(
        cursorSeq: 20,
        changes: [taskChange(rowId: 'task-1', rev: 20, title: 'Récent')],
      ),
    ).syncOnce(_household);

    await engineAnswering(
      page(
        cursorSeq: 21,
        changes: [taskChange(rowId: 'task-1', rev: 5, title: 'Ancien')],
      ),
    ).syncOnce(_household);

    expect((await task('task-1'))?.title, 'Récent');
  });

  test('a tombstone keeps the content and marks the row', () async {
    await engineAnswering(
      page(
        cursorSeq: 20,
        changes: [taskChange(rowId: 'task-1', rev: 20, title: 'Enterrée')],
      ),
    ).syncOnce(_household);

    await engineAnswering(
      page(
        cursorSeq: 21,
        changes: [
          taskChange(
            rowId: 'task-1',
            rev: 21,
            title: 'Enterrée',
            deleted: true,
          ),
        ],
      ),
    ).syncOnce(_household);

    final stored = await task('task-1');
    expect(stored?.deletedRev, 21);
    expect(
      stored?.title,
      'Enterrée',
      reason:
          'a tombstone keeps the content, so the row can still say what it was',
    );
    // And it is out of the open list.
    expect(await db.countTasks(_household), 0);
  });

  // --- rule two --------------------------------------------------------------

  // One unreadable row must never freeze a phone's whole synchronisation. An
  // entity this version has never heard of is exactly that case, and it is how
  // an older app survives a newer server.
  test(
    'an unknown entity is set aside and the cursor still advances',
    () async {
      final report = await engineAnswering(
        page(
          cursorSeq: 99,
          changes: [
            taskChange(rowId: 'task-1', rev: 97),
            {
              'entity': 'quantum_chore',
              'row_id': 'row-x',
              'rev': 98,
              'deleted': false,
              'state': {'whatever': true},
            },
          ],
        ),
      ).syncOnce(_household);

      expect(report.quarantined, 1);
      expect(report.applied, 1, reason: 'the readable row must still land');
      expect(await db.quarantinedCount(), 1);
      expect(
        (await db.cursorFor(_household))?.cursorSeq,
        99,
        reason: 'a row set aside must not hold the cursor back',
      );
    },
  );

  test('the row set aside is kept verbatim for a later version', () async {
    await engineAnswering(
      page(
        cursorSeq: 99,
        changes: [
          {
            'entity': 'quantum_chore',
            'row_id': 'row-x',
            'rev': 98,
            'deleted': false,
            'state': {'colour': 'teal'},
          },
        ],
      ),
    ).syncOnce(_household);

    final aside = await db.select(db.quarantine).getSingle();
    expect(aside.raw, contains('teal'));
    expect(aside.entity, 'quantum_chore');
  });

  // --- what the server says about the cursor ---------------------------------

  test('an expired cursor is reported, not swallowed', () async {
    final report = await engineAnswering(
      page(cursorSeq: 500, snapshotRequired: true),
    ).syncOnce(_household);

    expect(report.snapshotRequired, isTrue);
  });

  test('being offline is not an error worth shouting about', () async {
    final engine = SyncEngine(
      db: db,
      api: SyncApi(
        httpClient: MockClient(
          (_) async => throw http.ClientException('no network'),
        ),
        baseUrl: 'http://example.invalid',
      ),
      now: () => 0,
    );

    await expectLater(
      engine.syncOnce(_household),
      throwsA(
        isA<SyncException>().having(
          (e) => e.failure,
          'failure',
          SyncFailure.offline,
        ),
      ),
    );
  });

  // Signing out and having the device removed must not look alike: only the
  // second one wipes the local replica.
  test('a revoked device is told apart from an expired session', () async {
    SyncEngine engineAnsweringStatus(int status, String body) {
      return SyncEngine(
        db: db,
        api: SyncApi(
          httpClient: MockClient((_) async => http.Response(body, status)),
          baseUrl: 'http://example.invalid',
        ),
        now: () => 0,
      );
    }

    await expectLater(
      engineAnsweringStatus(
        401,
        '{"error":"device_revoked"}',
      ).syncOnce(_household),
      throwsA(
        isA<SyncException>().having(
          (e) => e.failure,
          'failure',
          SyncFailure.deviceRevoked,
        ),
      ),
    );

    await expectLater(
      engineAnsweringStatus(
        401,
        '{"error":"unauthenticated"}',
      ).syncOnce(_household),
      throwsA(
        isA<SyncException>().having(
          (e) => e.failure,
          'failure',
          SyncFailure.unauthenticated,
        ),
      ),
    );
  });
}
