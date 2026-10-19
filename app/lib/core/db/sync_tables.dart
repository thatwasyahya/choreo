import 'package:drift/drift.dart';

/// The local mirrors of the server's synchronised tables.
///
/// Two rules govern every table in this file, and breaking either one is how a
/// phone silently stops synchronising:
///
/// **1. No CHECK constraint and no foreign key on a synchronised column.**
/// Pages arrive in revision order, not causal order: a task can reference a
/// category that has not been delivered yet, and a newer server can send a
/// value an older app has never heard of. The screen shows "no category", not
/// an exception.
///
/// That rule is why booleans here are [IntColumn] and not [BoolColumn], and why
/// no text column uses `withLength`: drift renders both as CHECK constraints,
/// and a constraint on a synchronised column turns a forward-compatible value
/// into a crash.
///
/// **2. Every row carries `pendingFields`.** When a server state arrives the
/// client writes every field EXCEPT the ones listed there. It is one column, it
/// costs nothing, and it is the only reliable defence against synchronisation
/// revoking the sentence the user is in the middle of typing.
mixin SyncedRow on Table {
  /// Minted by the client — a phone in a cellar has to create a row with no
  /// server in reach.
  TextColumn get id => text()();

  /// The tenant. Mirrored locally because one account can belong to several
  /// households, and a query that forgets it shows somebody else's chores.
  TextColumn get householdId => text()();

  /// The server's revision, which doubles as this row's position in the
  /// household's log.
  IntColumn get rev => integer()();
  IntColumn get createdRev => integer().withDefault(const Constant(0))();

  /// `{"title": 48102}` — per-field revisions, as JSON.
  TextColumn get fieldRevs => text().withDefault(const Constant('{}'))();

  /// A tombstone. The content columns are kept, so the row can still say what
  /// the thing was called.
  IntColumn get deletedRev => integer().nullable()();

  TextColumn get updatedBy => text().nullable()();
  TextColumn get updatedAt => text().nullable()();

  /// Fields this version of the app has no column for. Kept, never dropped: an
  /// older app must not destroy a newer one's data by round-tripping it.
  TextColumn get ext => text().withDefault(const Constant('{}'))();

  /// JSON array of fields the user has changed and the server has not yet
  /// acknowledged. See rule 2 above.
  TextColumn get pendingFields => text().withDefault(const Constant('[]'))();

  /// 1 for an occurrence predicted locally and not yet confirmed.
  IntColumn get provisional => integer().withDefault(const Constant(0))();

  @override
  Set<Column> get primaryKey => {householdId, id};
}

class Tasks extends Table with SyncedRow {
  /// 'task' or 'event'. Deliberately not an enum: a newer server may send a
  /// kind this app does not know, and the list must still render.
  TextColumn get kind => text().withDefault(const Constant('task'))();

  TextColumn get title => text().withDefault(const Constant(''))();
  TextColumn get notes => text().nullable()();

  /// A DATE, stored as text. Not an instant: "bin day is Tuesday" must not
  /// shift because somebody travelled.
  TextColumn get dueOn => text().nullable()();
  TextColumn get dueTime => text().nullable()();

  /// Integer, not boolean: see the note on CHECK constraints above.
  IntColumn get allDay => integer().withDefault(const Constant(1))();

  TextColumn get assigneeId => text().nullable()();
  TextColumn get categoryId => text().nullable()();
  IntColumn get priority => integer().nullable()();

  TextColumn get seriesId => text().nullable()();

  /// An integer, never a date. The occurrence id derives from it.
  IntColumn get seriesIndex => integer().nullable()();

  /// JSON arrays. Completions and tags travel INSIDE the task's state and have
  /// no revision of their own — ticking a task bumps the task's rev — so they
  /// are stored here rather than in tables that would need their own cursor.
  TextColumn get overrides => text().withDefault(const Constant('[]'))();
  TextColumn get completions => text().withDefault(const Constant('[]'))();
  TextColumn get tags => text().withDefault(const Constant('[]'))();
}

class SeriesRows extends Table with SyncedRow {
  TextColumn get title => text().withDefault(const Constant(''))();
  TextColumn get mode => text().withDefault(const Constant('calendar'))();
  TextColumn get rule => text().withDefault(const Constant(''))();
  IntColumn get relDays => integer().nullable()();
  TextColumn get anchorOn => text().nullable()();

  TextColumn get rotation => text().withDefault(const Constant('[]'))();
  IntColumn get rotationOffset => integer().withDefault(const Constant(0))();
  IntColumn get paused => integer().withDefault(const Constant(0))();
  TextColumn get catchUp => text().withDefault(const Constant('skip'))();

  /// Arbitrated by the server. The client never writes these two — it only
  /// mirrors them, and that absence is what keeps the invariant the server's
  /// business alone.
  IntColumn get nextIndex => integer().withDefault(const Constant(0))();
  TextColumn get openOccurrenceId => text().nullable()();
}

class Members extends Table with SyncedRow {
  TextColumn get role => text().withDefault(const Constant('adult'))();
  IntColumn get removed => integer().withDefault(const Constant(0))();

  /// display_name, colour, … as JSON.
  TextColumn get fields => text().withDefault(const Constant('{}'))();
}

/// Named `Checklist` and not `List` on purpose: drift derives the row class
/// from the table, and a class called `List` shadows `dart:core`'s. Every
/// `List<T>` in the app would stop compiling, with an error that blames the
/// wrong file.
@DataClassName('Checklist')
class Lists extends Table with SyncedRow {
  TextColumn get name => text().withDefault(const Constant(''))();
  TextColumn get kind => text().withDefault(const Constant('checklist'))();
}

class ListItems extends Table with SyncedRow {
  TextColumn get listId => text().nullable()();
  TextColumn get label => text().withDefault(const Constant(''))();
  TextColumn get qty => text().nullable()();
  TextColumn get unit => text().nullable()();

  /// Fractional, so moves interleave instead of fighting.
  TextColumn get orderKey => text().withDefault(const Constant(''))();
}

class Memos extends Table with SyncedRow {
  TextColumn get title => text().withDefault(const Constant(''))();
  TextColumn get body => text().withDefault(const Constant(''))();
  IntColumn get pinned => integer().withDefault(const Constant(0))();
}

/// The local mirror of the server's notices, so the "to review" tray renders
/// with no network.
///
/// A notice lives on the server precisely so it survives a reinstall and shows
/// up on every device of the same member; this is only a copy for display.
class Notices extends Table with SyncedRow {
  TextColumn get forMember => text().withDefault(const Constant(''))();
  TextColumn get kind => text().withDefault(const Constant(''))();

  /// A localisation key, never a sentence.
  TextColumn get code => text().withDefault(const Constant(''))();

  TextColumn get targetEntity => text().nullable()();
  TextColumn get targetRowId => text().nullable()();

  /// The user's own content, verbatim. This is what "put mine back" replays.
  TextColumn get mine => text().withDefault(const Constant('{}'))();
  TextColumn get theirs => text().nullable()();

  TextColumn get byMember => text().nullable()();
  TextColumn get opId => text().nullable()();

  IntColumn get resolved => integer().withDefault(const Constant(0))();

  /// Local only: whether the person has looked at the card. Never sent.
  IntColumn get seen => integer().withDefault(const Constant(0))();
}
