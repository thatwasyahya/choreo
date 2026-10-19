import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/features/check/check_screen.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:drift/drift.dart' show Value;
import 'package:drift/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

Widget _wrap(Widget child, {Locale? locale}) {
  return MaterialApp(
    locale: locale,
    localizationsDelegates: AppLocalizations.localizationsDelegates,
    supportedLocales: AppLocalizations.supportedLocales,
    home: child,
  );
}

ApiClient _answering(String body) {
  return ApiClient(
    httpClient: MockClient((_) async => http.Response(body, 200)),
    baseUrl: 'http://example.invalid',
  );
}

ApiClient _offline() {
  return ApiClient(
    httpClient: MockClient((_) async => throw http.ClientException('offline')),
    baseUrl: 'http://example.invalid',
  );
}

void main() {
  late AppDatabase db;

  setUp(() => db = AppDatabase(NativeDatabase.memory()));
  tearDown(() => db.close());

  Future<void> addTask(String title) {
    return db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'task-$title',
            householdId: 'home',
            rev: 1,
            title: Value(title),
          ),
        );
  }

  testWidgets('reports the server version when the server answers', (
    tester,
  ) async {
    await tester.pumpWidget(
      _wrap(
        CheckScreen(
          database: db,
          api: _answering('{"status":"ok","version":"1.2.3"}'),
          householdId: 'home',
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('1.2.3'), findsOneWidget);
  });

  // The whole point of the product: no network, still useful.
  testWidgets('still shows the local data with no network', (tester) async {
    await addTask('Poubelles');
    await addTask('Plombier');

    await tester.pumpWidget(
      _wrap(
        CheckScreen(database: db, api: _offline(), householdId: 'home'),
        locale: const Locale('fr'),
      ),
    );
    await tester.pumpAndSettle();

    final l10n = await AppLocalizations.delegate.load(const Locale('fr'));
    expect(find.text(l10n.serverUnreachable), findsOneWidget);
    // Read from the phone's own database, with the server unreachable.
    expect(find.text(l10n.tasksStored(2)), findsOneWidget);
  });

  // The outbox size is shown instead of a spinner: "3 gestures to send" is
  // honest about what is happening, an hourglass is not.
  testWidgets('shows how many gestures are waiting to go up', (tester) async {
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

    await tester.pumpWidget(
      _wrap(
        CheckScreen(database: db, api: _offline(), householdId: 'home'),
        locale: const Locale('fr'),
      ),
    );
    await tester.pumpAndSettle();

    final l10n = await AppLocalizations.delegate.load(const Locale('fr'));
    expect(find.text(l10n.pendingToSend(1)), findsOneWidget);
  });

  // Rows set aside never block synchronisation, so a permanent zero would be
  // noise. They appear only when there is something to say.
  testWidgets('mentions rows set aside only when there are some', (
    tester,
  ) async {
    await tester.pumpWidget(
      _wrap(
        CheckScreen(database: db, api: _offline(), householdId: 'home'),
        locale: const Locale('fr'),
      ),
    );
    await tester.pumpAndSettle();

    final l10n = await AppLocalizations.delegate.load(const Locale('fr'));
    expect(find.text(l10n.quarantinedRows(0)), findsNothing);

    await db
        .into(db.quarantine)
        .insert(
          QuarantineCompanion.insert(
            householdId: 'home',
            entity: 'task',
            rowId: 'row-1',
            rev: 1,
            raw: '{}',
            error: 'unreadable',
            atElapsed: 0,
          ),
        );

    await tester.tap(find.text(l10n.retry));
    await tester.pumpAndSettle();

    expect(find.text(l10n.quarantinedRows(1)), findsOneWidget);
  });

  testWidgets('renders in French', (tester) async {
    await tester.pumpWidget(
      _wrap(
        CheckScreen(database: db, api: _offline()),
        locale: const Locale('fr'),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Vérification'), findsOneWidget);
  });

  testWidgets('renders in English', (tester) async {
    await tester.pumpWidget(
      _wrap(
        CheckScreen(database: db, api: _offline()),
        locale: const Locale('en'),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Check'), findsOneWidget);
  });

  // No string typed into a widget: every label comes from the ARB files.
  testWidgets('carries no hard-coded label', (tester) async {
    await tester.pumpWidget(
      _wrap(
        CheckScreen(database: db, api: _offline()),
        locale: const Locale('en'),
      ),
    );
    await tester.pumpAndSettle();

    // "Vérification" is the French title. Seeing it under an English locale
    // would mean a label was written straight into the widget tree.
    expect(find.text('Vérification'), findsNothing);
  });
}
