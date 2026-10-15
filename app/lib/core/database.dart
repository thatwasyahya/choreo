import 'package:drift/drift.dart';
import 'package:drift_flutter/drift_flutter.dart';

part 'database.g.dart';

/// Every check the app has made, written down on the phone.
///
/// Deliberately trivial: phase 0 only has to prove that the app owns a real
/// database of its own rather than a cache. The household model arrives in
/// phase 1, once docs/SYNC_PROTOCOL.md is settled.
class Observations extends Table {
  IntColumn get id => integer().autoIncrement()();
  DateTimeColumn get observedAt => dateTime()();
  TextColumn get serverStatus => text().withLength(min: 1, max: 32)();
}

/// The local database.
///
/// This is the app's source of truth, not a copy of the server's. Everything
/// on screen is read from here, which is what makes every screen keep working
/// with no network (principle 9).
@DriftDatabase(tables: [Observations])
class AppDatabase extends _$AppDatabase {
  AppDatabase([QueryExecutor? executor])
    : super(executor ?? driftDatabase(name: 'choreo'));

  @override
  int get schemaVersion => 1;

  Future<void> record(String serverStatus) async {
    await into(observations).insert(
      ObservationsCompanion.insert(
        observedAt: DateTime.now(),
        serverStatus: serverStatus,
      ),
    );
  }

  Future<int> countObservations() async {
    final query = selectOnly(observations)
      ..addColumns([observations.id.count()]);
    final row = await query.getSingle();
    return row.read(observations.id.count()) ?? 0;
  }

  /// Ordered by insertion, not by timestamp.
  ///
  /// Two observations a few milliseconds apart can carry the same stored
  /// instant, and a phone's clock is not something we get to trust — a child's
  /// handset is set to whatever they felt like. The autoincrement id is the
  /// only monotonic thing here.
  Future<Observation?> latestObservation() {
    return (select(observations)
          ..orderBy([(o) => OrderingTerm.desc(o.id)])
          ..limit(1))
        .getSingleOrNull();
  }
}
