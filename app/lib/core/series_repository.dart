import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:drift/drift.dart';
import 'package:uuid/uuid.dart';

/// How a chore repeats, as somebody describes it rather than as a rule string.
///
/// The interface asks "every week, on Tuesday" and this turns it into the
/// RRULE subset the server honours. Building the string in a screen would put
/// the vocabulary in nine places; building it here keeps it in one.
class Repeat {
  const Repeat._({
    required this.mode,
    this.rule = '',
    this.relDays,
  });

  /// Every day, or every N days.
  factory Repeat.daily({int every = 1}) {
    return Repeat._(mode: 'calendar', rule: _rule('DAILY', every));
  }

  /// On the given weekdays — "bin day is Tuesday", "the gym on Monday and
  /// Thursday" — repeating every week, or every N weeks.
  factory Repeat.weekly({required Set<int> weekdays, int every = 1}) {
    final codes = <String>[];
    for (final day in [
      DateTime.monday,
      DateTime.tuesday,
      DateTime.wednesday,
      DateTime.thursday,
      DateTime.friday,
      DateTime.saturday,
      DateTime.sunday,
    ]) {
      if (weekdays.contains(day)) {
        codes.add(_codes[day]!);
      }
    }

    final rule = StringBuffer(_rule('WEEKLY', every));
    if (codes.isNotEmpty) {
      rule.write(';BYDAY=${codes.join(',')}');
    }
    return Repeat._(mode: 'calendar', rule: rule.toString());
  }

  /// On a day of the month, every month or every N months. The 31st means the
  /// end of the month: the server clamps it rather than skipping February.
  factory Repeat.monthly({required int dayOfMonth, int every = 1}) {
    return Repeat._(
      mode: 'calendar',
      rule: '${_rule('MONTHLY', every)};BYMONTHDAY=$dayOfMonth',
    );
  }

  /// A number of days after the chore was last actually DONE — "the sheets,
  /// three weeks after the last time" — which only a tick can anchor.
  factory Repeat.after({required int days}) {
    return Repeat._(mode: 'relative', relDays: days);
  }

  final String mode;
  final String rule;
  final int? relDays;

  static String _rule(String freq, int every) {
    return every <= 1 ? 'FREQ=$freq' : 'FREQ=$freq;INTERVAL=$every';
  }

  static const Map<int, String> _codes = {
    DateTime.monday: 'MO',
    DateTime.tuesday: 'TU',
    DateTime.wednesday: 'WE',
    DateTime.thursday: 'TH',
    DateTime.friday: 'FR',
    DateTime.saturday: 'SA',
    DateTime.sunday: 'SU',
  };
}

/// Making a chore repeat.
///
/// Offline like every other gesture: the series and its queued intent are
/// written in ONE transaction, so a crash between them cannot leave a chore
/// that exists locally and will never reach the household.
///
/// What it deliberately does NOT do is open the first occurrence. That is the
/// server's invariant — exactly one open occurrence, with a derived identity —
/// and a client that predicted it here would be a client that can break it.
/// The occurrence comes back in the same round trip.
class SeriesRepository {
  SeriesRepository({
    required this.db,
    required this.householdId,
    required this.memberId,
    String Function()? newId,
    int Function()? elapsed,
    DateTime Function()? wallClock,
  }) : _newId = newId ?? _uuidV7,
       _elapsed = elapsed ?? _defaultElapsed,
       _wallClock = wallClock ?? DateTime.now;

  final AppDatabase db;
  final String householdId;
  final String memberId;

  final String Function() _newId;
  final int Function() _elapsed;
  final DateTime Function() _wallClock;

  static String _uuidV7() => const Uuid().v7();

  static int _defaultElapsed() => DateTime.now().millisecondsSinceEpoch;

  /// Everything this household repeats, newest first.
  Stream<List<SeriesRow>> watchAll() {
    return (db.select(db.seriesRows)
          ..where((s) => s.householdId.equals(householdId))
          ..where((s) => s.deletedRev.isNull())
          ..orderBy([(s) => OrderingTerm.desc(s.rev)]))
        .watch();
  }

  /// Make a chore repeat, starting on [startsOn].
  Future<String> create({
    required String title,
    required Repeat repeat,
    required DateTime startsOn,
    List<String> rotation = const [],
    bool accumulate = false,
  }) async {
    final id = _newId();
    final anchor = _dateOnly(startsOn);
    final catchUp = accumulate ? 'accumulate' : 'skip';

    final fields = <String, dynamic>{
      'title': title,
      'mode': repeat.mode,
      'rule': repeat.rule,
      'anchor_on': anchor,
      'catch_up': catchUp,
      if (repeat.relDays != null) 'rel_days': repeat.relDays,
      if (rotation.isNotEmpty) 'rotation': rotation,
    };

    await db.transaction(() async {
      await db
          .into(db.seriesRows)
          .insert(
            SeriesRowsCompanion.insert(
              id: id,
              householdId: householdId,
              rev: await _nextLocalRev(),
              title: Value(title),
              mode: Value(repeat.mode),
              rule: Value(repeat.rule),
              relDays: Value(repeat.relDays),
              anchorOn: Value(anchor),
              catchUp: Value(catchUp),
              rotation: Value(jsonEncode(rotation)),
              updatedBy: Value(memberId),
              pendingFields: const Value('["title"]'),
            ),
          );

      await db
          .into(db.outbox)
          .insert(
            OutboxCompanion.insert(
              householdId: householdId,
              opId: _newId(),
              kind: 'create',
              entity: 'series',
              rowId: id,
              payload: jsonEncode({'fields': fields}),
              clientTs: Value(_wallClock().millisecondsSinceEpoch),
              enqueuedElapsed: _elapsed(),
            ),
          );
    });

    return id;
  }

  /// A creation carries the household's current position rather than zero, so
  /// it does not jump above rows the server has already numbered.
  Future<int> _nextLocalRev() async {
    final cursor = await db.cursorFor(householdId);
    return cursor?.cursorSeq ?? 0;
  }

  /// A DATE, never an instant: "it starts on the 27th" must not move because
  /// somebody set it up while travelling.
  static String _dateOnly(DateTime moment) {
    final month = moment.month.toString().padLeft(2, '0');
    final day = moment.day.toString().padLeft(2, '0');
    return '${moment.year}-$month-$day';
  }
}
