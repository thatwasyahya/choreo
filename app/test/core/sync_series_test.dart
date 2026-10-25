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

/// A server that sends one page and nothing else.
SyncApi _serving(Map<String, dynamic> body) {
  return SyncApi(
    baseUrl: 'http://example.invalid',
    httpClient: MockClient(
      (_) async => http.Response(
        jsonEncode(body),
        200,
        headers: {'content-type': 'application/json'},
      ),
    ),
  );
}

Map<String, dynamic> _page(List<Map<String, dynamic>> changes) => {
  'server_now': '2026-10-25T10:00:00Z',
  'head_seq': 40,
  'results': <Map<String, dynamic>>[],
  'changes': changes,
  'cursor_seq': 40,
  'has_more': false,
};

void main() {
  late AppDatabase db;

  setUp(() => db = AppDatabase(NativeDatabase.memory()));
  tearDown(() => db.close());

  Future<void> signedIn() async {
    await db.into(db.deviceIdentity).insert(
      const DeviceIdentityCompanion(accessToken: Value('access-1')),
    );
  }

  // Before this, a series row met the default branch and went to quarantine:
  // the app could not show a recurring chore at all, and the counter that is
  // supposed to mean "an app and a server have drifted apart" rose on an
  // ordinary household going about its week.
  test('a series arrives in the replica rather than in quarantine', () async {
    await signedIn();

    final engine = SyncEngine(
      db: db,
      api: _serving(
        _page([
          {
            'entity': 'series',
            'row_id': 'series-1',
            'rev': 40,
            'deleted': false,
            'state': {
              'title': 'Sortir les poubelles',
              'mode': 'calendar',
              'rule': 'FREQ=WEEKLY;BYDAY=TU',
              'anchor_on': '2026-10-27',
              'catch_up': 'skip',
              'rotation': ['member-me', 'member-sara'],
              'rotation_offset': 1,
              'paused': false,
              'next_index': 3,
              'open_occurrence_id': 'occurrence-3',
              'field_revs': {'title': 40},
            },
          },
        ]),
      ),
      now: () => 0,
    );

    final report = await engine.syncOnce(_household);

    expect(report.quarantined, 0, reason: 'a series is not an unknown entity');
    expect(report.applied, 1);

    final stored = await db.select(db.seriesRows).getSingle();
    expect(stored.title, 'Sortir les poubelles');
    expect(stored.rule, 'FREQ=WEEKLY;BYDAY=TU');
    expect(stored.anchorOn, '2026-10-27');
    expect(jsonDecode(stored.rotation), ['member-me', 'member-sara']);
    expect(stored.rotationOffset, 1);
  });

  // The two columns the client mirrors and never writes. They are the
  // invariant the server arbitrates, and they are here so a screen can say
  // "the next one is open" without asking the network.
  test('the arbitrated columns are mirrored', () async {
    await signedIn();

    final engine = SyncEngine(
      db: db,
      api: _serving(
        _page([
          {
            'entity': 'series',
            'row_id': 'series-1',
            'rev': 40,
            'deleted': false,
            'state': {
              'title': 'Poubelles',
              'next_index': 7,
              'open_occurrence_id': 'occurrence-7',
            },
          },
        ]),
      ),
      now: () => 0,
    );

    await engine.syncOnce(_household);

    final stored = await db.select(db.seriesRows).getSingle();
    expect(stored.nextIndex, 7);
    expect(stored.openOccurrenceId, 'occurrence-7');
  });

  // The same rule as everywhere else: an incoming state writes every field
  // EXCEPT the ones this phone is still holding, so synchronisation cannot
  // revoke the title somebody is in the middle of typing.
  test('an incoming series does not overwrite a field being edited', () async {
    await signedIn();
    await db.into(db.seriesRows).insert(
      SeriesRowsCompanion.insert(
        id: 'series-1',
        householdId: _household,
        rev: 10,
        title: const Value('Mon titre'),
        pendingFields: const Value('["title"]'),
      ),
    );

    final engine = SyncEngine(
      db: db,
      api: _serving(
        _page([
          {
            'entity': 'series',
            'row_id': 'series-1',
            'rev': 40,
            'deleted': false,
            'state': {'title': 'Leur titre', 'rule': 'FREQ=DAILY'},
          },
        ]),
      ),
      now: () => 0,
    );

    await engine.syncOnce(_household);

    final stored = await db.select(db.seriesRows).getSingle();
    expect(stored.title, 'Mon titre', reason: 'held until the server answers');
    expect(
      stored.rule,
      'FREQ=DAILY',
      reason: 'the other fields of the same state are applied, or the rule '
          'would degenerate into a frozen row',
    );
  });

  test('an older series state is not applied', () async {
    await signedIn();
    await db.into(db.seriesRows).insert(
      SeriesRowsCompanion.insert(
        id: 'series-1',
        householdId: _household,
        rev: 90,
        title: const Value('Récent'),
      ),
    );

    final engine = SyncEngine(
      db: db,
      api: _serving(
        _page([
          {
            'entity': 'series',
            'row_id': 'series-1',
            'rev': 40,
            'deleted': false,
            'state': {'title': 'Ancien'},
          },
        ]),
      ),
      now: () => 0,
    );

    await engine.syncOnce(_household);

    final stored = await db.select(db.seriesRows).getSingle();
    expect(stored.title, 'Récent');
  });
}
