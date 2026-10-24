import 'dart:async';

import 'package:choreo/core/session.dart';
import 'package:choreo/design/tokens.dart';
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
          padding: const EdgeInsets.symmetric(
            horizontal: Space.gutter,
            vertical: Space.room,
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // The name sits high and the actions sit low, with the air
              // between them rather than around the whole block. Centring
              // everything left a third of the page empty underneath and made
              // the composition look like a dialog that had lost its box.
              const Spacer(flex: 3),
              Text(
                l10n.appTitle,
                textAlign: TextAlign.center,
                style: theme.textTheme.displaySmall,
              ),
              const SizedBox(height: Space.step),
              Text(
                l10n.welcomeTagline,
                textAlign: TextAlign.center,
                style: theme.textTheme.bodyLarge?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              const Spacer(flex: 4),
              FilledButton(
                onPressed: () =>
                    unawaited(_open(context, CredentialsMode.signUp)),
                child: Text(l10n.createAccount),
              ),
              const SizedBox(height: Space.snug),
              TextButton(
                onPressed: () =>
                    unawaited(_open(context, CredentialsMode.signIn)),
                child: Text(l10n.haveAccount),
              ),
              const SizedBox(height: Space.snug),
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
