import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/features/check/check_screen.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

void main() {
  runApp(ChoreoApp(database: AppDatabase(), api: ApiClient()));
}

class ChoreoApp extends StatelessWidget {
  const ChoreoApp({required this.database, required this.api, super.key});

  final AppDatabase database;
  final ApiClient api;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      // Through the localisations, so the task switcher shows the right name.
      onGenerateTitle: (context) => AppLocalizations.of(context).appTitle,
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      theme: ThemeData(colorSchemeSeed: _seed),
      darkTheme: ThemeData(colorSchemeSeed: _seed, brightness: Brightness.dark),
      home: CheckScreen(database: database, api: api),
    );
  }
}

/// A calm green. The product says "to catch up", never "late" (principle 6),
/// and the colours are not allowed to contradict the words.
const Color _seed = Color(0xFF3F6C51);
