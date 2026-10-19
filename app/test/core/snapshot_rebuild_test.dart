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

  /// An engine whose server streams exactly these NDJSON lines.
  SyncEngine engineStreaming(List<String> lines) {
    return SyncEngine(
      db: db,
      api: SyncApi(
        httpClient: MockClient(
          (_) async => http.Response(lines.join('\n'), 200),
        ),
        baseUrl: 'http://example.invalid',
      ),
      now: () => 0,
    );
  }

  String header({int cursorSeq = 100, int purgeHorizonSeq = 0}) {
    return jsonEncode({
      'type': 'header',
      'cursor_seq': cursorSeq,
      'purge_horizon_seq': purgeHorizonSeq,
      'tz': 'Europe/Paris',
      'protocol': 1,
    });
  }

  String taskLine(String rowId, {int rev = 90, String title = 'Poubelles'}) {
    return jsonEncode({
      'type': 'task',
      'row_id': rowId,
      'rev': rev,
      'state': {
        'kind': 'task',
        'title': title,
        'all_day': true,
        'field_revs': {'title': rev},
        'ext': <String, dynamic>{},
        'completions': <dynamic>[],
        'tags': <dynamic>[],
      },
    });
  }

  String end(int rows) => jsonEncode({'type': 'end', 'rows': rows});

  Future<void> localTask(String rowId, {int rev = 10}) {
    return db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: rowId,
            householdId: _household,
            rev: rev,
            title: Value('locale $rowId'),
          ),
        );
  }

  test('a snapshot rebuilds the household and sets the cursor', () async {
    final report = await engineStreaming([
      header(),
      taskLine('task-1'),
      taskLine('task-2'),
      end(2),
    ]).rebuild(_household);

    expect(report.applied, 2);
    expect(report.cursorSeq, 100);
    expect(await db.countTasks(_household), 2);
    expect((await db.cursorFor(_household))?.cursorSeq, 100);
  });

  // The closed world. This is how a row deleted a hundred days ago finally
  // leaves a phone that was away, and it is why the server can purge for real.
  test('a row the snapshot has never heard of is dropped', () async {
    await localTask('ancienne');

    final report = await engineStreaming([
      header(),
      taskLine('task-1'),
      end(1),
    ]).rebuild(_household);

    expect(report.removed, 1);
    final remaining = await db.openTasks(_household);
    expect(remaining.map((t) => t.id), ['task-1']);
  });

  // "Your cursor expired" must never mean "your offline edits are lost".
  test('a row the outbox still references survives the sweep', () async {
    await localTask('en-attente');
    await db
        .into(db.outbox)
        .insert(
          OutboxCompanion.insert(
            householdId: _household,
            opId: 'op-1',
            kind: 'set_fields',
            entity: 'task',
            rowId: 'en-attente',
            payload: '{"title":"mon titre"}',
            enqueuedElapsed: 0,
          ),
        );

    final report = await engineStreaming([
      header(),
      taskLine('task-1'),
      end(1),
    ]).rebuild(_household);

    expect(report.removed, 0);
    expect(await db.countTasks(_household), 2);
    expect(
      await db.pendingCount(),
      1,
      reason: 'a rebuild must not touch the queue',
    );
  });

  // The reason the stream carries an explicit end line. Once the first byte is
  // out the status is already 200, so a failure can only truncate — and in a
  // closed world a truncated stream reads as "everything else was deleted".
  test('a truncated stream is refused rather than applied', () async {
    await localTask('ne-pas-perdre');

    await expectLater(
      engineStreaming([
        header(),
        taskLine('task-1'),
        // No end line: the connection died here.
      ]).rebuild(_household),
      throwsA(
        isA<SyncException>().having(
          (e) => e.failure,
          'failure',
          SyncFailure.unreadable,
        ),
      ),
    );

    // Nothing was applied and, above all, nothing was deleted.
    final remaining = await db.openTasks(_household);
    expect(remaining.map((t) => t.id), ['ne-pas-perdre']);
    expect(await db.cursorFor(_household), isNull);
  });

  // The declared count is a second, cheaper check on the same thing.
  test('a stream whose count does not match is refused', () async {
    await expectLater(
      engineStreaming([
        header(),
        taskLine('task-1'),
        end(7),
      ]).rebuild(_household),
      throwsA(isA<SyncException>()),
    );

    expect(await db.countTasks(_household), 0);
  });

  // A rebuild must not eat the sentence being typed either.
  test('a rebuild leaves a field being edited alone', () async {
    await db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'task-1',
            householdId: _household,
            rev: 10,
            title: const Value('Ce que je tape'),
            pendingFields: const Value('["title"]'),
          ),
        );

    await engineStreaming([
      header(),
      taskLine('task-1', title: 'Ce que le serveur dit'),
      end(1),
    ]).rebuild(_household);

    final stored = await (db.select(
      db.tasks,
    )..where((t) => t.id.equals('task-1'))).getSingle();
    expect(stored.title, 'Ce que je tape');
    expect(stored.rev, 90, reason: 'the rest of the row still caught up');
  });

  // An entity this version has never heard of is set aside, and the rebuild
  // still completes: a newer server must not be able to stop an older app.
  test('an unknown entity is set aside without failing the rebuild', () async {
    final report = await engineStreaming([
      header(),
      taskLine('task-1'),
      jsonEncode({
        'type': 'quantum_chore',
        'row_id': 'row-x',
        'rev': 91,
        'state': {'colour': 'teal'},
      }),
      end(2),
    ]).rebuild(_household);

    expect(report.applied, 1);
    expect(report.quarantined, 1);
    expect(await db.quarantinedCount(), 1);
    expect((await db.cursorFor(_household))?.cursorSeq, 100);
  });
}
