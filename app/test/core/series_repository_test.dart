import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/series_repository.dart';
import 'package:drift/drift.dart' show OrderingTerm;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';

const _household = 'home';
const _me = 'member-me';

void main() {
  late AppDatabase db;
  late SeriesRepository repo;

  setUp(() {
    db = AppDatabase(NativeDatabase.memory());

    var counter = 0;
    repo = SeriesRepository(
      db: db,
      householdId: _household,
      memberId: _me,
      newId: () => 'id-${counter++}',
      elapsed: () => 0,
      wallClock: () => DateTime.utc(2026, 10, 25, 10),
    );
  });
  tearDown(() => db.close());

  Future<OutboxData> queued() {
    return (db.select(
      db.outbox,
    )..orderBy([(o) => OrderingTerm.asc(o.lseq)])).getSingle();
  }

  Map<String, dynamic> fieldsOf(OutboxData entry) {
    final payload = jsonDecode(entry.payload) as Map<String, dynamic>;
    return payload['fields'] as Map<String, dynamic>;
  }

  // --- the vocabulary --------------------------------------------------------

  // The interface asks "every week, on Tuesday"; the rule string is built in
  // one place rather than in every screen that offers a repeat.
  test('a weekly repeat becomes the rule the server honours', () {
    expect(
      Repeat.weekly(weekdays: {DateTime.tuesday}).rule,
      'FREQ=WEEKLY;BYDAY=TU',
    );
    expect(
      Repeat.weekly(weekdays: {DateTime.monday, DateTime.thursday}).rule,
      'FREQ=WEEKLY;BYDAY=MO,TH',
    );
    expect(
      Repeat.weekly(weekdays: {DateTime.tuesday}, every: 2).rule,
      'FREQ=WEEKLY;INTERVAL=2;BYDAY=TU',
    );
  });

  test('the weekdays come out in the order of the week', () {
    expect(
      Repeat.weekly(
        weekdays: {DateTime.sunday, DateTime.monday, DateTime.friday},
      ).rule,
      'FREQ=WEEKLY;BYDAY=MO,FR,SU',
      reason: 'a set has no order, and a rule that shuffles reads as a '
          'different rule every time it is written',
    );
  });

  test('a daily repeat says nothing it does not need to', () {
    expect(Repeat.daily().rule, 'FREQ=DAILY');
    expect(Repeat.daily(every: 3).rule, 'FREQ=DAILY;INTERVAL=3');
  });

  test('a monthly repeat carries its day', () {
    expect(Repeat.monthly(dayOfMonth: 1).rule, 'FREQ=MONTHLY;BYMONTHDAY=1');
  });

  // "Three weeks after the last time", which only a tick can anchor, so there
  // is no rule at all.
  test('a relative repeat has no rule', () {
    final repeat = Repeat.after(days: 21);

    expect(repeat.mode, 'relative');
    expect(repeat.rule, isEmpty);
    expect(repeat.relDays, 21);
  });

  // --- the gesture -----------------------------------------------------------

  test('a series is visible at once and queued once', () async {
    final id = await repo.create(
      title: 'Sortir les poubelles',
      repeat: Repeat.weekly(weekdays: {DateTime.tuesday}),
      startsOn: DateTime.utc(2026, 10, 27),
    );

    final stored = await (db.select(
      db.seriesRows,
    )..where((s) => s.id.equals(id))).getSingle();
    expect(stored.title, 'Sortir les poubelles');
    expect(stored.rule, 'FREQ=WEEKLY;BYDAY=TU');
    expect(stored.anchorOn, '2026-10-27');

    final op = await queued();
    expect(op.kind, 'create');
    expect(op.entity, 'series');
    expect(op.rowId, id);
    expect(fieldsOf(op)['rule'], 'FREQ=WEEKLY;BYDAY=TU');
  });

  // The start date is a DATE: "it starts on the 27th" must not move because
  // somebody set the chore up while travelling.
  test('the start is a date, not an instant', () async {
    await repo.create(
      title: 'Poubelles',
      repeat: Repeat.daily(),
      startsOn: DateTime.utc(2026, 10, 27, 23, 45),
    );

    expect(fieldsOf(await queued())['anchor_on'], '2026-10-27');
  });

  test('a rotation travels with the series', () async {
    await repo.create(
      title: 'Vaisselle',
      repeat: Repeat.daily(),
      startsOn: DateTime.utc(2026, 10, 27),
      rotation: const ['member-me', 'member-sara'],
    );

    expect(fieldsOf(await queued())['rotation'], [
      'member-me',
      'member-sara',
    ]);
  });

  test('a relative series carries its interval and no rule', () async {
    await repo.create(
      title: 'Changer les draps',
      repeat: Repeat.after(days: 21),
      startsOn: DateTime.utc(2026, 10, 27),
    );

    final fields = fieldsOf(await queued());
    expect(fields['mode'], 'relative');
    expect(fields['rel_days'], 21);
    expect(fields['rule'], isEmpty);
  });

  test('catching up is a choice the household makes', () async {
    await repo.create(
      title: 'Relever le compteur',
      repeat: Repeat.monthly(dayOfMonth: 1),
      startsOn: DateTime.utc(2026, 11),
      accumulate: true,
    );

    expect(fieldsOf(await queued())['catch_up'], 'accumulate');
  });

  // The invariant is the server's: exactly one open occurrence, with a derived
  // identity. A client that predicted it here would be a client that can break
  // it, so the first occurrence arrives in the answer.
  test('the client does not invent the first occurrence', () async {
    await repo.create(
      title: 'Poubelles',
      repeat: Repeat.daily(),
      startsOn: DateTime.utc(2026, 10, 27),
    );

    expect(await db.select(db.tasks).get(), isEmpty);
  });

  // One transaction per gesture: a crash between the row and the queue entry
  // would either lose the chore or show one that will never reach the
  // household, and the second is worse because nobody can tell.
  test('a gesture never leaves half of itself behind', () async {
    final id = await repo.create(
      title: 'Poubelles',
      repeat: Repeat.daily(),
      startsOn: DateTime.utc(2026, 10, 27),
    );

    final op = await queued();
    expect(op.rowId, id);
    expect(await db.select(db.seriesRows).get(), hasLength(1));
  });
}
