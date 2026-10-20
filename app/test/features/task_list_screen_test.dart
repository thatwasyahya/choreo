import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/notice_repository.dart';
import 'package:choreo/core/task_repository.dart';
import 'package:choreo/features/tasks/task_list_screen.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:drift/drift.dart' show Value;
import 'package:drift/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/screen.dart';

const _household = 'home';
const _me = 'member-me';

/// French unless a test says otherwise.
///
/// Without an explicit locale a widget test runs in the platform's, which is
/// English here — and every assertion about a French sentence would be looking
/// for a string the app was right not to show.
Widget _wrap(Widget child, {Locale? locale}) {
  return MaterialApp(
    locale: locale ?? const Locale('fr'),
    localizationsDelegates: AppLocalizations.localizationsDelegates,
    supportedLocales: AppLocalizations.supportedLocales,
    home: child,
  );
}

void main() {
  late AppDatabase db;
  late TaskRepository repo;
  late NoticeRepository notices;

  // No network and no mock of one. This screen reads the local database and
  // nothing else, which is why it behaves the same in a cellar.
  setUp(() {
    db = AppDatabase(NativeDatabase.memory());

    var counter = 0;
    String nextId() => 'id-${counter++}';

    repo = TaskRepository(
      db: db,
      householdId: _household,
      memberId: _me,
      newId: nextId,
      elapsed: () => 0,
      wallClock: () => DateTime.utc(2026, 10, 19, 14, 30),
    );
    notices = NoticeRepository(
      db: db,
      householdId: _household,
      memberId: _me,
      newId: nextId,
      elapsed: () => 0,
      wallClock: () => DateTime.utc(2026, 10, 19, 14, 30),
    );
  });
  tearDown(() => db.close());

  Future<void> show(WidgetTester tester, {Locale? locale}) async {
    await tester.pumpWidget(
      _wrap(
        TaskListScreen(repository: repo, notices: notices),
        locale: locale,
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<List<Task>> stored() => db.select(db.tasks).get();

  Future<List<OutboxData>> queued() => db.select(db.outbox).get();

  // --- the ten-second criterion ---------------------------------------------

  // Product principle 1, measured rather than hoped for. Two taps and one typed
  // title, with no other interaction: open the sheet, type, send. Anything that
  // adds a third decision breaks this test, which is the point of having it.
  screenTest('a task takes one tap, a title and the keyboard', (tester) async {
    await show(tester);

    await tester.tap(find.byType(FloatingActionButton));
    await tester.pumpAndSettle();

    final field = find.byType(TextField);
    expect(
      tester.widget<TextField>(field).autofocus,
      isTrue,
      reason: 'the keyboard has to be up already, or ten seconds is a wish',
    );

    await tester.enterText(field, 'Sortir les poubelles');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await tester.pumpAndSettle();

    expect((await stored()).single.title, 'Sortir les poubelles');
    expect(find.text('Sortir les poubelles'), findsOneWidget);
  });

  screenTest('only the title is asked for', (tester) async {
    await show(tester);

    await tester.tap(find.byType(FloatingActionButton));
    await tester.pumpAndSettle();

    expect(
      find.byType(TextField),
      findsOneWidget,
      reason: 'a second field is a second decision, and it costs the ten '
          'seconds',
    );
  });

  screenTest('the send button creates the same way the keyboard does', (
    tester,
  ) async {
    await show(tester);

    await tester.tap(find.byType(FloatingActionButton));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'Lait');
    await tester.tap(find.byIcon(Icons.arrow_upward));
    await tester.pumpAndSettle();

    expect((await stored()).single.title, 'Lait');
  });

  screenTest('an empty title creates nothing', (tester) async {
    await show(tester);

    await tester.tap(find.byType(FloatingActionButton));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), '   ');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await tester.pumpAndSettle();

    expect(await stored(), isEmpty);
    expect(await queued(), isEmpty);
  });

  // --- the list --------------------------------------------------------------

  screenTest('the list catches up on its own', (tester) async {
    await show(tester);
    expect(find.text("Rien à faire pour l'instant."), findsOneWidget);

    // Written behind the screen's back, the way the sync engine writes.
    await repo.create('Arrosé les plantes');
    await tester.pumpAndSettle();

    expect(find.text('Arrosé les plantes'), findsOneWidget);
  });

  screenTest('ticking a task records a completion', (tester) async {
    await repo.create('Poubelles');
    await show(tester);

    await tester.tap(find.byType(Checkbox));
    await tester.pumpAndSettle();

    final completions =
        (jsonDecode((await stored()).single.completions) as List<dynamic>)
            .cast<Map<String, dynamic>>();
    expect(completions.single['member_id'], _me);
    expect(tester.widget<Checkbox>(find.byType(Checkbox)).value, isTrue);
  });

  screenTest('unticking is possible in the same gesture', (tester) async {
    await repo.create('Poubelles');
    await show(tester);

    await tester.tap(find.byType(Checkbox));
    await tester.pumpAndSettle();
    await tester.tap(find.byType(Checkbox));
    await tester.pumpAndSettle();

    final completions =
        jsonDecode((await stored()).single.completions) as List<dynamic>;
    expect(completions, isEmpty);
  });

  screenTest('swiping a task away buries it', (tester) async {
    await repo.create('Acheter du lait');
    await show(tester);

    await tester.drag(find.byType(Dismissible), const Offset(-500, 0));
    await tester.pumpAndSettle();

    expect(find.text('Acheter du lait'), findsNothing);
    expect(
      (await stored()).single.deletedRev,
      isNotNull,
      reason: 'the row leaves the list but keeps what it was called',
    );
  });

  // A deleted task is a tombstone, not an absence: the row stays in the table
  // and a query that forgets it shows the dead.
  screenTest('a buried task does not come back on the next frame', (
    tester,
  ) async {
    final id = await repo.create('Déjà enterrée');
    await (db.update(db.tasks)..where((t) => t.id.equals(id))).write(
      const TasksCompanion(deletedRev: Value(1)),
    );

    await show(tester);

    expect(find.text('Déjà enterrée'), findsNothing);
  });

  // Another household's chores are not ours, and the screen is the last place
  // that boundary can leak.
  screenTest('another household stays out of the list', (tester) async {
    await db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'theirs',
            householdId: 'somebody-else',
            rev: 1,
            title: const Value('Leur vaisselle'),
          ),
        );

    await show(tester);

    expect(find.text('Leur vaisselle'), findsNothing);
  });

  // --- the review tray -------------------------------------------------------

  // A badge standing at zero is a standing invitation to worry, so there is no
  // button at all until something is actually waiting.
  screenTest('no tray button when there is nothing to review', (tester) async {
    await show(tester);

    expect(find.byIcon(Icons.inbox_outlined), findsNothing);
  });

  screenTest('a waiting card puts the tray within reach', (tester) async {
    await db
        .into(db.notices)
        .insert(
          NoticesCompanion.insert(
            id: 'notice-1',
            householdId: _household,
            rev: 7,
            forMember: const Value(_me),
            code: const Value('field_superseded'),
          ),
        );

    await show(tester);

    expect(find.byIcon(Icons.inbox_outlined), findsOneWidget);
  });

  // --- the words -------------------------------------------------------------

  // Principle 6: nothing in this product reproaches anybody, and the empty
  // state is where a reproach would appear first.
  screenTest('the empty state asks for nothing', (tester) async {
    await show(tester, locale: const Locale('en'));

    expect(find.text('Nothing to do right now.'), findsOneWidget);
  });
}
