import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/features/check/check_screen.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
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

  testWidgets('reports the server version when the server answers', (
    tester,
  ) async {
    await tester.pumpWidget(
      _wrap(
        CheckScreen(
          database: db,
          api: _answering('{"status":"ok","version":"1.2.3"}'),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('1.2.3'), findsOneWidget);
  });

  // The whole point of the product: no network, still useful.
  testWidgets('still shows the local data with no network', (tester) async {
    // The locale is pinned so the expected strings and the rendered ones come
    // from the same language. Without it the widget falls back to the test
    // environment's locale and the comparison is meaningless.
    await tester.pumpWidget(
      _wrap(
        CheckScreen(database: db, api: _offline()),
        locale: const Locale('fr'),
      ),
    );
    await tester.pumpAndSettle();

    final l10n = await AppLocalizations.delegate.load(const Locale('fr'));
    expect(find.text(l10n.serverUnreachable), findsOneWidget);
    // The observation was recorded even though the server never replied.
    expect(find.text(l10n.observationsRecorded(1)), findsOneWidget);
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
