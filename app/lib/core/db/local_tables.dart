import 'package:drift/drift.dart';

/// The tables that never leave the phone.
///
/// Constraints are welcome here, unlike in sync_tables.dart: nothing in this
/// file ever receives a value from a newer server, so a CHECK can only catch
/// our own bugs.

/// The queue of gestures made offline, waiting to go up.
///
/// Replay order comes from an autoincrementing integer and NEVER from a
/// timestamp. On a phone the autoincrement is the only monotonic thing there
/// is: the wall clock moves backwards when the network corrects it, and a
/// child's handset is set to whatever they felt like.
class Outbox extends Table {
  IntColumn get lseq => integer().autoIncrement()();

  TextColumn get householdId => text()();

  /// Minted at the moment of the gesture, and the key to end-to-end
  /// idempotency: a retry of the same entry reaches the same answer.
  TextColumn get opId => text().unique()();

  TextColumn get kind => text()();
  TextColumn get entity => text()();
  TextColumn get rowId => text()();

  /// JSON, and ONLY the fields that changed. Sending the whole row would make
  /// every edit a conflict with every other edit.
  TextColumn get payload => text()();

  /// `{"title": 48102}` — what the author actually had in front of them.
  TextColumn get baseRevs => text().withDefault(const Constant('{}'))();

  /// The wall clock, for display. Stored, never trusted.
  IntColumn get clientTs => integer().nullable()();

  /// Monotonic ticks, for backoff. Separate from clientTs on purpose: backoff
  /// computed from a wall clock goes haywire the moment the clock is corrected.
  IntColumn get enqueuedElapsed => integer()();

  TextColumn get state => text().withDefault(const Constant('queued'))();
  IntColumn get attempts => integer().withDefault(const Constant(0))();
  TextColumn get lastError => text().nullable()();
}

/// One cursor PER HOUSEHOLD.
///
/// Not a single row with `CHECK (id = 1)`: a carer looking after a parent's
/// household, and alternating custody, are facts of the domain. Wiring them in
/// now costs a primary key; adding them later means reworking the sync engine
/// after it is in service.
class SyncCursors extends Table {
  TextColumn get householdId => text()();

  IntColumn get cursorSeq => integer().withDefault(const Constant(0))();
  IntColumn get purgeHorizonSeq => integer().withDefault(const Constant(0))();
  IntColumn get protocol => integer().withDefault(const Constant(1))();

  TextColumn get minAppVersion => text().nullable()();
  IntColumn get sunsetAt => integer().nullable()();

  /// How far this phone's wall clock is from the server's. Survives a restart,
  /// and is only ever used to DISPLAY times sensibly — never to decide who won
  /// a conflict.
  IntColumn get serverWallOffsetMs => integer().nullable()();

  /// More precise, and dies at reboot: an anchor between the server's clock and
  /// this boot's monotonic ticks.
  IntColumn get serverMonoAnchorMs => integer().nullable()();
  TextColumn get bootId => text().nullable()();

  IntColumn get lastSyncElapsed => integer().nullable()();
  TextColumn get lastOutcome => text().nullable()();

  @override
  Set<Column> get primaryKey => {householdId};
}

/// The single row that says who this installation is.
class DeviceIdentity extends Table {
  IntColumn get id => integer().withDefault(const Constant(1))();

  /// Minted by the SERVER. It carries the sync cursor, so a client able to
  /// claim another device's id would corrupt that device's replica.
  TextColumn get deviceId => text().nullable()();

  TextColumn get accessToken => text().nullable()();
  TextColumn get refreshToken => text().nullable()();

  @override
  Set<Column> get primaryKey => {id};
}

/// A row of server state we could not apply.
///
/// It NEVER blocks the cursor. One unreadable row must not freeze a phone's
/// entire synchronisation in silence — that is precisely the failure the rule
/// "the server always serves the last two app versions" exists to prevent, and
/// the failure a frozen cursor would reintroduce from the other side.
///
/// The count of these rows is the only alarm worth raising about it.
class Quarantine extends Table {
  IntColumn get id => integer().autoIncrement()();

  TextColumn get householdId => text()();
  TextColumn get entity => text()();
  TextColumn get rowId => text()();
  IntColumn get rev => integer()();

  /// The state verbatim, so the row can be applied later by a version that
  /// understands it, or read by a human at two in the morning.
  TextColumn get raw => text()();

  TextColumn get error => text()();
  IntColumn get atElapsed => integer()();
}
