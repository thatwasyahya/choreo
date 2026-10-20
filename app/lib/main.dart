import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/features/shell/app_shell.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

void main() {
  final db = AppDatabase();
  final auth = AuthApi();

  runApp(
    ChoreoApp(
      database: db,
      auth: auth,
      api: ApiClient(),
      sessions: SessionRepository(db: db, auth: auth),
    ),
  );
}

class ChoreoApp extends StatelessWidget {
  const ChoreoApp({
    required this.database,
    required this.auth,
    required this.api,
    required this.sessions,
    super.key,
  });

  final AppDatabase database;
  final AuthApi auth;
  final ApiClient api;
  final SessionRepository sessions;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      // Through the localisations, so the task switcher shows the right name.
      onGenerateTitle: (context) => AppLocalizations.of(context).appTitle,
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      theme: ThemeData(colorSchemeSeed: _seed),
      darkTheme: ThemeData(colorSchemeSeed: _seed, brightness: Brightness.dark),
      home: AppShell(
        db: database,
        sessions: sessions,
        auth: auth,
        api: api,
      ),
    );
  }
}

/// A calm green. The product says "to catch up", never "late" (principle 6),
/// and the colours are not allowed to contradict the words.
const Color _seed = Color(0xFF3F6C51);
