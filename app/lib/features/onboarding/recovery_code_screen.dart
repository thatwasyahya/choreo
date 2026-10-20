import 'dart:async';

import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// The code, once.
///
/// The server keeps only a hash of it and can never show it again. With no
/// e-mail address in this product it is the only way back into an account, so
/// this screen refuses to be left until the person has said they wrote it
/// down — the one place in Choreo where a confirmation is worth the friction.
class RecoveryCodeScreen extends StatefulWidget {
  const RecoveryCodeScreen({required this.code, super.key});

  final String code;

  @override
  State<RecoveryCodeScreen> createState() => _RecoveryCodeScreenState();
}

class _RecoveryCodeScreenState extends State<RecoveryCodeScreen> {
  bool _noted = false;
  bool _copied = false;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.recoveryTitle),
        // No back arrow, and the system gesture is caught below: leaving here
        // without the code means losing the account.
        automaticallyImplyLeading: false,
      ),
      body: PopScope(
        canPop: false,
        child: SafeArea(
          child: ListView(
            padding: const EdgeInsets.all(24),
            children: [
              Text(l10n.recoveryExplain, style: theme.textTheme.bodyLarge),
              const SizedBox(height: 24),
              Card(
                child: Padding(
                  padding: const EdgeInsets.all(20),
                  child: SelectableText(
                    widget.code,
                    textAlign: TextAlign.center,
                    style: theme.textTheme.headlineSmall?.copyWith(
                      // Crockford base32 has no I, L, O or U, so there is no
                      // ambiguous pair left to disambiguate — but a monospaced
                      // face still makes it readable in groups.
                      fontFamily: 'monospace',
                      letterSpacing: 2,
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 8),
              Align(
                child: TextButton.icon(
                  onPressed: () => unawaited(_copy()),
                  icon: const Icon(Icons.copy_outlined),
                  label: Text(_copied ? l10n.codeCopied : l10n.copyCode),
                ),
              ),
              const SizedBox(height: 16),
              CheckboxListTile(
                value: _noted,
                onChanged: (value) =>
                    setState(() => _noted = value ?? false),
                title: Text(l10n.recoveryNoted),
                controlAffinity: ListTileControlAffinity.leading,
              ),
              const SizedBox(height: 16),
              FilledButton(
                onPressed: _noted ? () => Navigator.of(context).pop() : null,
                child: Text(l10n.continueLabel),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _copy() async {
    await Clipboard.setData(ClipboardData(text: widget.code));
    if (!mounted) {
      return;
    }
    setState(() => _copied = true);
  }
}
