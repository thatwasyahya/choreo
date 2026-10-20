import 'dart:async';

import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/features/onboarding/auth_message.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// Found a household, or join one.
///
/// The second and last screen before the chores. A household is the tenant of
/// everything in this product, so there is no "skip" here — but there is
/// nothing to decide either: a name, or a code somebody read out loud.
class HouseholdSetupScreen extends StatefulWidget {
  const HouseholdSetupScreen({required this.sessions, super.key});

  final SessionRepository sessions;

  @override
  State<HouseholdSetupScreen> createState() => _HouseholdSetupScreenState();
}

class _HouseholdSetupScreenState extends State<HouseholdSetupScreen> {
  final TextEditingController _householdName = TextEditingController();
  final TextEditingController _displayName = TextEditingController();
  final TextEditingController _code = TextEditingController();

  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _householdName.dispose();
    _displayName.dispose();
    _code.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.householdTitle),
        actions: [
          TextButton(
            onPressed: _busy ? null : () => _run(widget.sessions.signOut),
            child: Text(l10n.signOut),
          ),
        ],
      ),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.all(24),
          children: [
            TextField(
              controller: _displayName,
              autofocus: true,
              textCapitalization: TextCapitalization.words,
              textInputAction: TextInputAction.next,
              decoration: InputDecoration(
                labelText: l10n.displayNameLabel,
                border: const OutlineInputBorder(),
              ),
            ),
            if (_error != null) ...[
              const SizedBox(height: 16),
              Text(
                _error!,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.error,
                ),
              ),
            ],
            const SizedBox(height: 32),
            Text(l10n.createHousehold, style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            TextField(
              controller: _householdName,
              textCapitalization: TextCapitalization.sentences,
              textInputAction: TextInputAction.done,
              decoration: InputDecoration(
                labelText: l10n.householdNameLabel,
                border: const OutlineInputBorder(),
              ),
              onSubmitted: (_) => _create(),
            ),
            const SizedBox(height: 12),
            FilledButton(
              onPressed: _busy ? null : _create,
              child: Text(l10n.createHousehold),
            ),
            const SizedBox(height: 40),
            Text(l10n.joinHousehold, style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            TextField(
              controller: _code,
              autocorrect: false,
              enableSuggestions: false,
              textCapitalization: TextCapitalization.characters,
              textInputAction: TextInputAction.done,
              decoration: InputDecoration(
                labelText: l10n.inviteCodeLabel,
                border: const OutlineInputBorder(),
              ),
              onSubmitted: (_) => _join(),
            ),
            const SizedBox(height: 12),
            OutlinedButton(
              onPressed: _busy ? null : _join,
              child: Text(l10n.joinHousehold),
            ),
          ],
        ),
      ),
    );
  }

  void _create() {
    final name = _householdName.text.trim();
    final me = _displayName.text.trim();
    if (name.isEmpty || me.isEmpty) {
      return;
    }

    _run(
      () => widget.sessions.createHousehold(name: name, displayName: me),
    );
  }

  void _join() {
    final code = _code.text.trim();
    final me = _displayName.text.trim();
    if (code.isEmpty || me.isEmpty) {
      return;
    }

    _run(() => widget.sessions.join(code: code, displayName: me));
  }

  /// One place where an attempt becomes either a new state or a sentence.
  ///
  /// Nothing to navigate to on success: the shell watches the local database
  /// and has already moved on by the time this returns.
  void _run(Future<void> Function() attempt) {
    if (_busy) {
      return;
    }

    setState(() {
      _busy = true;
      _error = null;
    });

    final l10n = AppLocalizations.of(context);

    unawaited(
      attempt().onError<AuthException>((error, _) {
        if (!mounted) {
          return;
        }
        setState(() {
          _busy = false;
          _error = messageFor(l10n, error.failure);
        });
      }),
    );
  }
}
