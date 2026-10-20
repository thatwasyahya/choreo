import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/notice_repository.dart';
import 'package:choreo/features/tasks/review_tray.dart';
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
  late NoticeRepository notices;

  void open() {
    db = AppDatabase(NativeDatabase.memory());
    addTearDown(db.close);

    var counter = 0;
    notices = NoticeRepository(
      db: db,
      householdId: _household,
      memberId: _me,
      newId: () => 'id-${counter++}',
      elapsed: () => 0,
      wallClock: () => DateTime.utc(2026, 10, 19, 14, 30),
    );
  }

  Future<void> addNotice({
    String id = 'notice-1',
    String code = 'field_superseded',
    Map<String, dynamic> mine = const {'title': 'Mon titre'},
    Map<String, dynamic>? theirs = const {'title': 'Leur titre'},
    String? targetRowId = 'task-1',
  }) {
    return db
        .into(db.notices)
        .insert(
          NoticesCompanion.insert(
            id: id,
            householdId: _household,
            rev: 10,
            forMember: const Value(_me),
            kind: const Value('receipt'),
            code: Value(code),
            targetEntity: const Value('task'),
            targetRowId: Value(targetRowId),
            mine: Value(jsonEncode(mine)),
            theirs: Value(theirs == null ? null : jsonEncode(theirs)),
          ),
        );
  }

  Future<void> addTask() {
    return db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'task-1',
            householdId: _household,
            rev: 9,
            title: const Value('Leur titre'),
          ),
        );
  }

  Future<void> show(WidgetTester tester, {Locale? locale}) async {
    await tester.pumpWidget(
      _wrap(ReviewTray(notices: notices), locale: locale),
    );
    await tester.pumpAndSettle();
  }

  screenTest('an empty tray says so plainly', (tester) async {
    open();
    await show(tester);

    expect(find.text('Rien à revoir.'), findsOneWidget);
  });

  // The card carries the author's text verbatim. That is the whole reason to
  // show it rather than log it.
  screenTest('a card shows both texts, word for word', (tester) async {
    open();
    await addTask();
    await addNotice();

    await show(tester);

    expect(
      find.text("Quelqu'un a écrit autre chose au même moment."),
      findsOneWidget,
    );
    expect(find.text('Mon titre'), findsOneWidget);
    expect(find.text('Leur titre'), findsOneWidget);
  });

  screenTest('putting the text back rewrites and closes the card', (
    tester,
  ) async {
    open();
    await addTask();
    await addNotice();

    await show(tester);
    await tester.tap(find.text('Remettre mon texte'));
    await tester.pumpAndSettle();

    final rewritten = await (db.select(
      db.tasks,
    )..where((t) => t.id.equals('task-1'))).getSingle();
    expect(rewritten.title, 'Mon titre');
    expect(find.text('Rien à revoir.'), findsOneWidget);
  });

  screenTest('closing a card is enough to make it leave', (tester) async {
    open();
    await addTask();
    await addNotice();

    await show(tester);
    await tester.tap(find.text('Fermer'));
    await tester.pumpAndSettle();

    expect(find.text('Rien à revoir.'), findsOneWidget);
    expect(await db.select(db.outbox).get(), hasLength(1));
  });

  // A newer server may send a code this version has never heard of. Silence
  // would be worse than a vague line: the person did work that did not land.
  screenTest('an unknown code still earns a card', (tester) async {
    open();
    await addTask();
    await addNotice(code: 'something_this_version_never_heard_of');

    await show(tester);

    expect(
      find.text("Quelque chose n'a pas pu se faire comme demandé."),
      findsOneWidget,
    );
  });

  // Nothing to rewrite to: the offer is not made, and the card still closes.
  screenTest('no target means no offer to put the text back', (tester) async {
    open();
    await addNotice(targetRowId: null);

    await show(tester);

    expect(find.text('Remettre mon texte'), findsNothing);
    expect(find.text('Fermer'), findsOneWidget);
  });

  // The test that matters for the public repo: no French sentence may leak into
  // the English locale.
  screenTest('the English locale is English', (tester) async {
    open();
    await addTask();
    await addNotice();

    await show(tester, locale: const Locale('en'));

    expect(
      find.text('Somebody wrote something else at the same moment.'),
      findsOneWidget,
    );
    expect(find.text('Put my text back'), findsOneWidget);
  });
}
