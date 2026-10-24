import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/features/shell/app_shell.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:drift/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../support/fake_auth.dart';
import '../support/screen.dart';

ApiClient _offlineApi() {
  return ApiClient(
    httpClient: MockClient((_) async => throw http.ClientException('offline')),
    baseUrl: 'http://example.invalid',
  );
}

void main() {
  late AppDatabase db;
  final seen = <http.Request>[];

  setUp(() {
    db = AppDatabase(NativeDatabase.memory());
    seen.clear();
  });
  tearDown(() => db.close());

  Future<void> start(WidgetTester tester, {AuthApi? auth}) async {
    final api = auth ?? agreeableAuth(seen: seen);

    await tester.pumpWidget(
      MaterialApp(
        locale: const Locale('fr'),
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: AppShell(
          db: db,
          sessions: SessionRepository(
            db: db,
            auth: api,
            timeZone: () async => 'Europe/Paris',
            wallClock: () => DateTime.utc(2026, 10, 19, 14, 30),
          ),
          auth: api,
          api: _offlineApi(),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  /// An installation that signed in and founded a household some time ago.
  ///
  /// Through `runAsync`, and that is not a detail: a widget test's body runs on
  /// a fake clock, so awaiting a future made inside it — an HTTP call here —
  /// waits for a queue that only a pump would drain. Nothing pumps while the
  /// body is blocked, and the test hangs until the framework times it out ten
  /// minutes later. `runAsync` runs this on the real clock, where it belongs:
  /// this is the part that already happened.
  Future<void> signedInEarlier(WidgetTester tester) async {
    await tester.runAsync(() async {
      final sessions = SessionRepository(
        db: db,
        auth: agreeableAuth(seen: seen),
        timeZone: () async => 'Europe/Paris',
      );

      await sessions.signUp(login: 'yahya', password: 'un mot de passe');
      await sessions.createHousehold(name: 'Maison', displayName: 'Yahya');
    });
  }

  Future<void> signUp(WidgetTester tester) async {
    await tester.tap(find.text('Créer un compte'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField).first, 'yahya');
    await tester.enterText(find.byType(TextField).last, 'un mot de passe');
    await tester.tap(find.widgetWithText(FilledButton, 'Créer un compte'));
    await tester.pumpAndSettle();
  }

  // --- the way in ------------------------------------------------------------

  screenTest('a fresh install offers two doors and no tour', (tester) async {
    await start(tester);

    expect(find.text('Créer un compte'), findsOneWidget);
    expect(find.text("J'ai déjà un compte"), findsOneWidget);
  });

  // --- the recovery code -----------------------------------------------------

  // The server keeps only a hash of it and can never show it again. With no
  // e-mail address in this product it is the only way back into an account.
  screenTest('signing up shows the recovery code once', (tester) async {
    await start(tester);
    await signUp(tester);

    expect(find.text('K7MN-4PQR-8STV-2WXY-3Z5A-6BCD'), findsOneWidget);
  });

  screenTest('the code screen will not be left until it is written down', (
    tester,
  ) async {
    await start(tester);
    await signUp(tester);

    final carryOn = find.widgetWithText(FilledButton, 'Continuer');
    expect(tester.widget<FilledButton>(carryOn).onPressed, isNull);

    await tester.tap(find.byType(Checkbox));
    await tester.pumpAndSettle();

    expect(tester.widget<FilledButton>(carryOn).onPressed, isNotNull);
  });

  screenTest('no back arrow out of the code screen', (tester) async {
    await start(tester);
    await signUp(tester);

    expect(find.byType(BackButton), findsNothing);
  });

  // --- the household ---------------------------------------------------------

  screenTest('after the code comes the household', (tester) async {
    await start(tester);
    await signUp(tester);

    await tester.tap(find.byType(Checkbox));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Continuer'));
    await tester.pumpAndSettle();

    expect(find.text('Créer un foyer'), findsWidgets);
    expect(find.text('Rejoindre un foyer'), findsWidgets);
  });

  // The whole point of the shell: three states, decided by the local database.
  screenTest('founding a household lands on the chores', (tester) async {
    await start(tester);
    await signUp(tester);

    await tester.tap(find.byType(Checkbox));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Continuer'));
    await tester.pumpAndSettle();

    await tester.enterText(
      find.widgetWithText(TextField, 'Mon prénom dans le foyer'),
      'Yahya',
    );
    await tester.enterText(
      find.widgetWithText(TextField, 'Nom du foyer'),
      'Maison',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Créer un foyer'));
    await tester.pumpAndSettle();

    expect(find.text('À faire'), findsOneWidget);
    expect(find.byType(FloatingActionButton), findsOneWidget);
  });

  // --- what the server refuses ----------------------------------------------

  screenTest('a taken login is said on the spot', (tester) async {
    await start(tester, auth: refusingAuth(409, 'login_taken'));
    await signUp(tester);

    expect(find.text('Cet identifiant est déjà pris.'), findsOneWidget);
  });

  // Being offline is not an error in this product, and the wording says so.
  screenTest('an unreachable server is not a failure of the person', (
    tester,
  ) async {
    await start(tester, auth: unreachableAuth());
    await signUp(tester);

    expect(
      find.text(
        'Le serveur est injoignable. '
        "Rien n'est perdu : il suffit de réessayer plus tard.",
      ),
      findsOneWidget,
    );
  });

  // --- coming back -----------------------------------------------------------

  // The promise of the product in one test: a phone that has been signed in
  // once opens on its task list with no network at all.
  screenTest('a signed-in installation opens on the chores', (tester) async {
    // Signed in earlier, then the app was killed.
    await signedInEarlier(tester);

    // And comes back to a server that is not there any more.
    await start(tester, auth: unreachableAuth());

    expect(find.text('À faire'), findsOneWidget);
  });

  screenTest('signing out comes back to the way in', (tester) async {
    await signedInEarlier(tester);

    await start(tester);
    await tester.tap(find.byIcon(Icons.people_outline));
    await tester.pumpAndSettle();

    // Signing out moved into the overflow menu: a heading and two actions do
    // not fit across a phone, and these are the rarest things on that screen.
    await tester.tap(find.byIcon(Icons.more_horiz));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Se déconnecter'));
    await tester.pumpAndSettle();

    expect(find.text('Créer un compte'), findsOneWidget);
  });
}
