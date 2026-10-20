import 'dart:async';

import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/features/onboarding/auth_message.dart';
import 'package:choreo/features/onboarding/recovery_code_screen.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// Whether this screen is making an account or opening one.
enum CredentialsMode { signUp, signIn }

/// A login and a password. There is nothing else to ask for.
///
/// No e-mail address anywhere in this product (decision D-009): nothing to
/// confirm, nothing to leak, and no address to lose control of. The price is
/// paid once, on the next screen — the recovery code is the only way back in.
class CredentialsScreen extends StatefulWidget {
  const CredentialsScreen({
    required this.sessions,
    required this.mode,
    super.key,
  });

  final SessionRepository sessions;
  final CredentialsMode mode;

  @override
  State<CredentialsScreen> createState() => _CredentialsScreenState();
}

class _CredentialsScreenState extends State<CredentialsScreen> {
  final TextEditingController _login = TextEditingController();
  final TextEditingController _password = TextEditingController();

  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _login.dispose();
    _password.dispose();
    super.dispose();
  }

  bool get _isSignUp => widget.mode == CredentialsMode.signUp;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final title = _isSignUp ? l10n.createAccount : l10n.signIn;

    return Scaffold(
      appBar: AppBar(title: Text(title)),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.all(24),
          children: [
            TextField(
              controller: _login,
              autofocus: true,
              autocorrect: false,
              enableSuggestions: false,
              textInputAction: TextInputAction.next,
              decoration: InputDecoration(
                labelText: l10n.loginLabel,
                helperText: _isSignUp ? l10n.loginHint : null,
                helperMaxLines: 2,
                border: const OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _password,
              obscureText: true,
              autocorrect: false,
              enableSuggestions: false,
              textInputAction: TextInputAction.done,
              decoration: InputDecoration(
                labelText: l10n.passwordLabel,
                // The rule stated in advance, on sign-up only: being told
                // afterwards that it was too short is the server's job, not a
                // design.
                helperText: _isSignUp ? l10n.passwordHint : null,
                helperMaxLines: 2,
                border: const OutlineInputBorder(),
              ),
              onSubmitted: (_) => _submit(),
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
            const SizedBox(height: 24),
            FilledButton(
              onPressed: _busy ? null : _submit,
              child: Text(title),
            ),
          ],
        ),
      ),
    );
  }

  void _submit() {
    if (_busy) {
      return;
    }

    final login = _login.text.trim();
    final password = _password.text;
    if (login.isEmpty || password.isEmpty) {
      return;
    }

    setState(() {
      _busy = true;
      _error = null;
    });

    unawaited(_attempt(login, password));
  }

  Future<void> _attempt(String login, String password) async {
    final l10n = AppLocalizations.of(context);

    try {
      if (_isSignUp) {
        final code = await widget.sessions.signUp(
          login: login,
          password: password,
        );
        if (!mounted) {
          return;
        }
        // Replaced rather than pushed: going back to a filled-in sign-up form
        // after the account exists would only invite a second attempt.
        await Navigator.of(context).pushReplacement(
          MaterialPageRoute<void>(
            builder: (_) => RecoveryCodeScreen(code: code),
          ),
        );
        return;
      }

      await widget.sessions.signIn(login: login, password: password);
      if (!mounted) {
        return;
      }
      // Nothing to navigate to: the shell is watching the local database and
      // has already moved on.
      Navigator.of(context).pop();
    } on AuthException catch (error) {
      if (!mounted) {
        return;
      }
      setState(() {
        _busy = false;
        _error = messageFor(l10n, error.failure);
      });
    }
  }
}
