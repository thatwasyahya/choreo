
import 'package:choreo/core/database.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/core/sync/sync_api.dart';
import 'package:choreo/core/sync/sync_driver.dart';
import 'package:choreo/core/sync/sync_engine.dart';
import 'package:choreo/core/task_repository.dart';
import 'package:drift/drift.dart' show Value;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../support/fake_auth.dart';

const _household = 'home';
const _me = 'member-me';

/// A server that counts what it was asked, and can be told to misbehave.
class _Server {
  _Server();

  int calls = 0;
  int lastWaitMs = -1;
  SyncFailure? failWith;
  bool snapshotRequired = false;

  /// The ops of every call, so a test can prove a gesture was pushed once and
  /// not twice.
  final List<List<String>> pushed = [];

  SyncApi get api => SyncApi(
    baseUrl: 'http://example.invalid',
    httpClient: MockClient((request) async {
      calls++;

      final body = request.body;
      lastWaitMs = _intIn(body, 'wait_ms');
      pushed.add(_opIdsIn(body));

      final failure = failWith;
      if (failure != null) {
        switch (failure) {
          case SyncFailure.offline:
            throw http.ClientException('offline');
          case SyncFailure.unauthenticated:
            return http.Response('{"error":"unauthenticated"}', 401);
          case SyncFailure.deviceRevoked:
            return http.Response('{"error":"device_revoked"}', 401);
          case SyncFailure.unreadable:
            return http.Response('not json at all', 200);
        }
      }

      // A verdict for every operation sent. Answering with an empty list
      // would be a different test entirely: the engine is right to keep an
      // operation the server said nothing about.
      final verdicts = pushed.last
          .map((id) => '{"op_id":"$id","outcome":"applied","rev":1}')
          .join(',');

      return http.Response(
        '{"server_now":"2026-10-24T10:00:00Z","head_seq":1,'
        '"results":[$verdicts],"changes":[],"cursor_seq":1,"has_more":false,'
        '"snapshot_required":$snapshotRequired}',
        200,
        headers: {'content-type': 'application/json'},
      );
    }),
  );

  static int _intIn(String body, String key) {
    final match = RegExp('"$key":\\s*(\\d+)').firstMatch(body);
    return match == null ? -1 : int.parse(match.group(1)!);
  }

  static List<String> _opIdsIn(String body) {
    return RegExp('"op_id":"([^"]+)"')
        .allMatches(body)
        .map((m) => m.group(1)!)
        .toList();
  }
}

void main() {
  late AppDatabase db;
  late _Server server;
  late SessionRepository sessions;
  late SyncDriver driver;
  late TaskRepository tasks;
  var clock = 0;

  setUp(() async {
    db = AppDatabase(NativeDatabase.memory());
    server = _Server();
    clock = 0;

    sessions = SessionRepository(
      db: db,
      auth: agreeableAuth(),
      timeZone: () async => 'Europe/Paris',
    );

    driver = SyncDriver(
      db: db,
      engine: SyncEngine(db: db, api: server.api, now: () => clock),
      sessions: sessions,
      elapsed: () => clock,
      // Short, so a test does not wait on a real twenty seconds.
      foregroundWaitMs: 50,
    );

    // The same clock as the driver: an age computed from two different clocks
    // is a number with no meaning, which is exactly what the first run of
    // this test produced.
    tasks = TaskRepository(
      db: db,
      householdId: _household,
      memberId: _me,
      elapsed: () => clock,
    );

    // Signed in, with a household on screen.
    await db
        .into(db.deviceIdentity)
        .insert(
          const DeviceIdentityCompanion(
            id: Value(1),
            deviceId: Value('device-1'),
            accountId: Value('account-1'),
            accessToken: Value('access-1'),
            refreshToken: Value('refresh-1'),
          ),
        );
    await db
        .into(db.memberships)
        .insert(
          const MembershipsCompanion(
            householdId: Value(_household),
            memberId: Value(_me),
            role: Value('admin'),
            current: Value(1),
          ),
        );
  });

  tearDown(() async {
    await driver.dispose();
    await db.close();
  });

  // --- the gap this closes ---------------------------------------------------

  // The engine could make a round trip and nothing called it: every gesture
  // landed in the outbox and stayed there. This is the test that would have
  // failed before the driver existed.
  test('a queued gesture reaches the server without being asked', () async {
    await tasks.create('Sortir les poubelles');
    expect(await db.pendingCount(), 1, reason: 'queued, and nothing sent yet');

    driver.resumed();
    await pumpEventQueue(times: 30);

    expect(server.calls, greaterThan(0));
    expect(
      server.pushed.first,
      hasLength(1),
      reason: 'the gesture went up in the first round trip',
    );
  });

  test('the foreground call parks a long poll', () async {
    driver.resumed();
    await pumpEventQueue(times: 10);

    expect(
      server.lastWaitMs,
      50,
      reason: 'the waiting belongs to the server, which is what makes a '
          'write land in one round trip',
    );
  });

  // Off screen there is no parked call, so a zero delay would be a tight loop
  // against the server. Android kills a held socket anyway (D-010 rules out a
  // push service), so the background path is a periodic task, not this loop.
  test('a paused app does not spin', () async {
    driver.resumed();
    await pumpEventQueue(times: 10);
    final whilePaused = server.calls;

    driver.paused();
    await pumpEventQueue(times: 40);

    expect(
      server.calls - whilePaused,
      lessThanOrEqualTo(1),
      reason: 'at most the trip already in flight',
    );
  });

  // --- one at a time ---------------------------------------------------------

  // The protocol allows one batch in flight. Overlapping calls would push the
  // same operations twice and race on the cursor.
  test('two requests at once make one round trip', () async {
    await tasks.create('Poubelles');

    await Future.wait([driver.syncNow(), driver.syncNow(), driver.syncNow()]);

    expect(server.calls, 1);
  });

  test('a gesture is pushed once, not in every later call', () async {
    await tasks.create('Poubelles');

    await driver.syncNow();
    await driver.syncNow();

    expect(server.pushed.first, hasLength(1));
    expect(
      server.pushed.last,
      isEmpty,
      reason: 'settled operations leave the queue, so they stop being sent',
    );
  });

  // --- being offline ---------------------------------------------------------

  test('being offline is reported as itself, and the queue is kept', () async {
    server.failWith = SyncFailure.offline;
    await tasks.create('Poubelles');

    final seen = <SyncStatus>[];
    final watching = driver.watch().listen(seen.add);

    await driver.syncNow();
    await pumpEventQueue(times: 5);
    await watching.cancel();

    expect(seen.last.lastOutcome, 'offline');
    expect(seen.last.isOffline, isTrue);
    expect(seen.last.pending, 1, reason: 'nothing is dropped on a failure');
    expect(await db.pendingCount(), 1);
  });

  // What the screen shows instead of a spinner: a count and an age. A silent
  // queue stuck since Tuesday is the failure that makes people leave.
  test('the status carries the age of the oldest gesture', () async {
    server.failWith = SyncFailure.offline;

    clock = 1000;
    await tasks.create('Poubelles');
    clock = 1000 + 10 * 24 * 60 * 60 * 1000; // ten days later

    final seen = <SyncStatus>[];
    final watching = driver.watch().listen(seen.add);
    await driver.syncNow();
    await pumpEventQueue(times: 5);
    await watching.cancel();

    expect(seen.last.pending, 1);
    expect(seen.last.oldestPendingMs, 10 * 24 * 60 * 60 * 1000);
  });

  // --- credentials -----------------------------------------------------------

  test('an expired session is renewed and the trip retried', () async {
    server.failWith = SyncFailure.unauthenticated;
    await driver.syncNow();
    await pumpEventQueue(times: 10);

    final identity = await db.select(db.deviceIdentity).getSingleOrNull();
    expect(
      identity?.accessToken,
      'access-3',
      reason: 'the renewed pair from the fake server replaced the old one',
    );
  });

  // Signing out is not being revoked, and the difference is the whole point:
  // a device removed from a household must not keep a readable replica of it.
  test('a revoked device loses the replica, not just the tokens', () async {
    await tasks.create('Poubelles');
    await db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'from-the-server',
            householdId: _household,
            rev: 7,
            title: const Value('Vaisselle'),
          ),
        );

    server.failWith = SyncFailure.deviceRevoked;
    await driver.syncNow();
    await pumpEventQueue(times: 10);

    expect(await db.select(db.tasks).get(), isEmpty);
    expect(await db.pendingCount(), 0);
    expect(await db.select(db.memberships).get(), isEmpty);

    final identity = await db.select(db.deviceIdentity).getSingleOrNull();
    expect(identity?.accessToken, isNull);
  });

  // --- the reset path --------------------------------------------------------

  test('a cursor the server has purged triggers a rebuild', () async {
    server.snapshotRequired = true;

    final seen = <SyncStatus>[];
    final watching = driver.watch().listen(seen.add);
    await driver.syncNow();
    await pumpEventQueue(times: 10);
    await watching.cancel();

    expect(
      seen.any((s) => s.rebuilding),
      isTrue,
      reason: 'the person is told the replica is being rebuilt',
    );
  });

  // --- following the session -------------------------------------------------

  test('signing out stops the loop', () async {
    driver.follow();
    await pumpEventQueue(times: 10);
    final before = server.calls;

    await sessions.signOut();
    await pumpEventQueue(times: 30);

    expect(
      server.calls - before,
      lessThanOrEqualTo(1),
      reason: 'no household on screen, nothing to synchronise',
    );
  });
}
