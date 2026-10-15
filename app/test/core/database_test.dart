import 'package:choreo/core/database.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late AppDatabase db;

  // In memory: a test must never touch the real database on the machine.
  setUp(() => db = AppDatabase(NativeDatabase.memory()));
  tearDown(() => db.close());

  test('a fresh database holds nothing', () async {
    expect(await db.countObservations(), 0);
    expect(await db.latestObservation(), isNull);
  });

  test('an observation survives being written', () async {
    await db.record('reachable');

    expect(await db.countObservations(), 1);
    expect((await db.latestObservation())?.serverStatus, 'reachable');
  });

  test('the history is kept, and the newest one wins', () async {
    // Back to back on purpose: both land in the same stored second, so this
    // only passes because the order comes from the id, not the timestamp.
    await db.record('unreachable');
    await db.record('reachable');

    expect(await db.countObservations(), 2);
    expect((await db.latestObservation())?.serverStatus, 'reachable');
  });
}
