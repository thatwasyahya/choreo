import 'dart:async';

import 'package:choreo/core/session.dart';
import 'package:choreo/features/onboarding/credentials_screen.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// The way in. Two doors and nothing else.
///
/// No tour, no permissions to grant, no e-mail address to confirm: an account
/// here is a login and a password (decision D-009), and the fastest honest path
/// to a first task is two taps from here.
class WelcomeScreen extends StatelessWidget {
  const WelcomeScreen({required this.sessions, super.key});

  final SessionRepository sessions;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    return Scaffold(
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(
                l10n.appTitle,
                textAlign: TextAlign.center,
                style: theme.textTheme.displaySmall,
              ),
              const SizedBox(height: 8),
              Text(
                l10n.welcomeTagline,
                textAlign: TextAlign.center,
                style: theme.textTheme.bodyLarge,
              ),
              const SizedBox(height: 48),
              FilledButton(
                onPressed: () =>
                    unawaited(_open(context, CredentialsMode.signUp)),
                child: Text(l10n.createAccount),
              ),
              const SizedBox(height: 12),
              TextButton(
                onPressed: () =>
                    unawaited(_open(context, CredentialsMode.signIn)),
                child: Text(l10n.haveAccount),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _open(BuildContext context, CredentialsMode mode) {
    return Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => CredentialsScreen(sessions: sessions, mode: mode),
      ),
    );
  }
}
