import 'package:choreo/core/db/local_tables.dart';
import 'package:choreo/core/db/sync_tables.dart';
import 'package:drift/drift.dart';
import 'package:drift_flutter/drift_flutter.dart';

export 'package:choreo/core/db/local_tables.dart';
export 'package:choreo/core/db/sync_tables.dart';

part 'database.g.dart';

/// The local database.
///
/// This is the app's source of truth, not a copy of the server's. Everything on
/// screen is read from here, which is what makes every screen keep working with
/// no network — supermarket, cellar, underground, aeroplane.
///
/// The schema is in two halves, and the split is load-bearing:
///
///   - `db/sync_tables.dart` mirrors the server. No CHECK constraint, no
///     foreign key, because pages arrive in revision order rather than causal
///     order and a newer server may send a value this app has never heard of.
///   - `db/local_tables.dart` never leaves the phone, so constraints there can
///     only catch our own mistakes.
@DriftDatabase(
  tables: [
    // Mirrors of the server.
    Tasks,
    SeriesRows,
    Members,
    Lists,
    ListItems,
    Memos,
    Notices,
    // Local only.
    Outbox,
    SyncCursors,
    DeviceIdentity,
    Memberships,
    Quarantine,
  ],
)
class AppDatabase extends _$AppDatabase {
  AppDatabase([QueryExecutor? executor])
    : super(executor ?? driftDatabase(name: 'choreo'));

  @override
  int get schemaVersion => 3;

  @override
  MigrationStrategy get migration => MigrationStrategy(
    onCreate: (m) => m.createAll(),
    onUpgrade: (m, from, to) async {
      if (from < 2) {
        // Phase 0 shipped a single probe table to prove the plumbing. It was
        // scaffolding and never held anything a person typed, so dropping it
        // costs nobody anything.
        await m.database.customStatement('DROP TABLE IF EXISTS observations');
        await m.createAll();
      }
      if (from < 3) {
        // Who this installation is, per household. Added rather than inferred:
        // the task list needs it before any network call, and a fresh install
        // simply has no row yet.
        await m.createTable(memberships);
        await m.addColumn(deviceIdentity, deviceIdentity.accountId);
        await m.addColumn(deviceIdentity, deviceIdentity.login);
        await m.addColumn(deviceIdentity, deviceIdentity.accessUntil);
      }
    },
  );

  /// Tasks that are neither buried nor finished, newest first.
  ///
  /// Ordered by rev and not by any date: rev is the household's own clock, and
  /// it is the only ordering two phones are guaranteed to agree on.
  Future<List<Task>> openTasks(String householdId) {
    return (select(tasks)
          ..where((t) => t.householdId.equals(householdId))
          ..where((t) => t.deletedRev.isNull())
          ..orderBy([(t) => OrderingTerm.desc(t.rev)]))
        .get();
  }

  Future<int> countTasks(String householdId) async {
    final query = selectOnly(tasks)
      ..addColumns([tasks.id.count()])
      ..where(
        tasks.householdId.equals(householdId) & tasks.deletedRev.isNull(),
      );

    final row = await query.getSingle();
    return row.read(tasks.id.count()) ?? 0;
  }

  /// How many gestures are still waiting to go up.
  ///
  /// This is what the interface shows instead of a spinner: "three things to
  /// send" is honest, and it is also one of the success measures of the
  /// specification.
  Future<int> pendingCount() async {
    final query = selectOnly(outbox)..addColumns([outbox.lseq.count()]);
    final row = await query.getSingle();
    return row.read(outbox.lseq.count()) ?? 0;
  }

  /// Rows of server state this version could not apply.
  ///
  /// Never zero-by-construction, and never a reason to stop: the cursor moves
  /// on regardless. A count above zero is the one alarm worth raising, because
  /// it means an app and a server have drifted apart.
  Future<int> quarantinedCount() async {
    final query = selectOnly(quarantine)..addColumns([quarantine.id.count()]);
    final row = await query.getSingle();
    return row.read(quarantine.id.count()) ?? 0;
  }

  Future<SyncCursor?> cursorFor(String householdId) {
    return (select(
      syncCursors,
    )..where((c) => c.householdId.equals(householdId))).getSingleOrNull();
  }
}
