import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/core/sync/sync_api.dart';
import 'package:choreo/core/sync/sync_driver.dart';
import 'package:choreo/core/sync/sync_engine.dart';
import 'package:choreo/design/theme.dart';
import 'package:choreo/features/shell/app_shell.dart';
import 'package:choreo/features/shell/sync_host.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

void main() {
  final db = AppDatabase();
  final auth = AuthApi();
  final sessions = SessionRepository(db: db, auth: auth);

  runApp(
    ChoreoApp(
      database: db,
      auth: auth,
      api: ApiClient(),
      sessions: sessions,
      // The loop that drives the engine. Without it every gesture lands in the
      // outbox and stays there: the phone becomes a private notepad, and two
      // of them never converge.
      driver: SyncDriver(
        db: db,
        engine: SyncEngine(db: db, api: SyncApi()),
        sessions: sessions,
      ),
    ),
  );
}

class ChoreoApp extends StatelessWidget {
  const ChoreoApp({
    required this.database,
    required this.auth,
    required this.api,
    required this.sessions,
    required this.driver,
    super.key,
  });

  final AppDatabase database;
  final AuthApi auth;
  final ApiClient api;
  final SessionRepository sessions;
  final SyncDriver driver;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      // Through the localisations, so the task switcher shows the right name.
      onGenerateTitle: (context) => AppLocalizations.of(context).appTitle,
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      theme: ChoreoTheme.light,
      darkTheme: ChoreoTheme.dark,
      home: SyncHost(
        driver: driver,
        child: AppShell(
          db: database,
          sessions: sessions,
          auth: auth,
          api: api,
        ),
      ),
    );
  }
}
