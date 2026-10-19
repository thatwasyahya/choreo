import 'package:choreo/core/database.dart';
import 'package:drift/drift.dart';
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late AppDatabase db;

  // In memory: a test must never touch the real database on the device.
  setUp(() => db = AppDatabase(NativeDatabase.memory()));
  tearDown(() => db.close());

  /// The tables that receive state from the server.
  const synchronised = <String>[
    'tasks',
    'series_rows',
    'members',
    'lists',
    'list_items',
    'memos',
    'notices',
  ];

  Future<String> ddl(String table) async {
    final rows = await db
        .customSelect(
          "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
          variables: [Variable<String>(table)],
        )
        .get();
    return rows.single.read<String>('sql');
  }

  // Matched as SQL keywords, not as substrings. Searching for "CHECK" in an
  // upper-cased DDL finds it inside the default value 'checklist' and fails a
  // table that has no constraint at all.
  final checkConstraint = RegExp(r'\bCHECK\s*\(', caseSensitive: false);
  final foreignKey = RegExp(r'\bREFERENCES\b', caseSensitive: false);

  group('the schema of a synchronised table', () {
    // Rule one of the protocol, made mechanical. Pages arrive in revision
    // order, not causal order: a task can reference a category that has not
    // been delivered yet, and a newer server can send a value this app has
    // never heard of. A constraint here turns a forward-compatible value into
    // an exception, and the screen should have shown "no category".
    //
    // This test exists because the obvious way to write these tables —
    // boolean() for a flag, withLength() for a title — generates exactly the
    // CHECK constraints that break it.
    test('carries no CHECK constraint', () async {
      for (final table in synchronised) {
        expect(
          checkConstraint.hasMatch(await ddl(table)),
          isFalse,
          reason:
              '$table has a CHECK. A newer server sending an unknown value '
              'would raise instead of rendering.',
        );
      }
    });

    test('carries no foreign key', () async {
      for (final table in synchronised) {
        expect(
          foreignKey.hasMatch(await ddl(table)),
          isFalse,
          reason:
              '$table has a foreign key. Rows arrive in revision order, so the '
              'row it points at may simply not have been delivered yet.',
        );
      }
    });
  });

  // The behaviour the rule protects, rather than its shape.
  test(
    'a value this version has never heard of is stored, not refused',
    () async {
      await db
          .into(db.tasks)
          .insert(
            TasksCompanion.insert(
              id: 'task-1',
              householdId: 'home',
              rev: 10,
              // A newer server could introduce this. An older app must list it.
              kind: const Value('appointment'),
              title: const Value('Dentiste'),
            ),
          );

      final stored = await db.openTasks('home');
      expect(stored.single.kind, 'appointment');
    },
  );

  test('a field this version has no column for survives in ext', () async {
    await db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'task-1',
            householdId: 'home',
            rev: 10,
            title: const Value('Poubelles'),
            ext: const Value('{"colour":"teal"}'),
          ),
        );

    final stored = await db.openTasks('home');
    expect(stored.single.ext, contains('teal'));
  });

  group('the outbox', () {
    // Replay order comes from an autoincrement and NEVER from a timestamp. On
    // a phone the autoincrement is the only monotonic thing: the wall clock
    // moves backwards the moment the network corrects it.
    test('replays in the order gestures were made', () async {
      for (final title in ['premier', 'deuxième', 'troisième']) {
        await db
            .into(db.outbox)
            .insert(
              OutboxCompanion.insert(
                householdId: 'home',
                opId: 'op-$title',
                kind: 'create',
                entity: 'task',
                rowId: 'row-$title',
                payload: '{"title":"$title"}',
                // Deliberately decreasing: if replay order ever came from a
                // clock, this ordering would come out reversed.
                clientTs: Value(1000 - title.length),
                enqueuedElapsed: 0,
              ),
            );
      }

      final queued = await (db.select(
        db.outbox,
      )..orderBy([(o) => OrderingTerm.asc(o.lseq)])).get();

      expect(
        queued.map((o) => o.opId),
        ['op-premier', 'op-deuxième', 'op-troisième'],
      );
    });

    test('counts what is still waiting to go up', () async {
      expect(await db.pendingCount(), 0);

      await db
          .into(db.outbox)
          .insert(
            OutboxCompanion.insert(
              householdId: 'home',
              opId: 'op-1',
              kind: 'create',
              entity: 'task',
              rowId: 'row-1',
              payload: '{}',
              enqueuedElapsed: 0,
            ),
          );

      expect(await db.pendingCount(), 1);
    });
  });

  // Rule two: a row we could not apply goes aside and the cursor moves on. One
  // unreadable row must never freeze a phone's whole synchronisation in
  // silence.
  test('a quarantined row is counted but blocks nothing', () async {
    await db
        .into(db.quarantine)
        .insert(
          QuarantineCompanion.insert(
            householdId: 'home',
            entity: 'task',
            rowId: 'row-1',
            rev: 48314,
            raw: '{"something":"we could not read"}',
            error: 'unknown field type',
            atElapsed: 0,
          ),
        );

    expect(await db.quarantinedCount(), 1);

    // And the cursor is free to be anywhere, including past the bad row.
    await db
        .into(db.syncCursors)
        .insert(
          SyncCursorsCompanion.insert(
            householdId: 'home',
            cursorSeq: const Value(48400),
          ),
        );

    final cursor = await db.cursorFor('home');
    expect(cursor?.cursorSeq, 48400);
  });

  test('a cursor exists per household, not one for the device', () async {
    await db
        .into(db.syncCursors)
        .insert(
          SyncCursorsCompanion.insert(
            householdId: 'maison',
            cursorSeq: const Value(100),
          ),
        );
    await db
        .into(db.syncCursors)
        .insert(
          SyncCursorsCompanion.insert(
            householdId: 'chez-maman',
            cursorSeq: const Value(7),
          ),
        );

    expect((await db.cursorFor('maison'))?.cursorSeq, 100);
    expect((await db.cursorFor('chez-maman'))?.cursorSeq, 7);
  });
}
