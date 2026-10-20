import 'dart:async';

import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/features/onboarding/auth_message.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// Mint a code and show it.
///
/// One of the two places in the app that needs the network to say anything
/// useful — a code has to exist on the server before it can be read out — so
/// it is a sheet and not a screen: it fails, it says so, and nothing else in
/// the app is waiting on it.
Future<void> showInviteSheet(
  BuildContext context, {
  required AuthApi auth,
  required Session session,
}) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (context) => _InviteSheet(auth: auth, session: session),
  );
}

class _InviteSheet extends StatefulWidget {
  const _InviteSheet({required this.auth, required this.session});

  final AuthApi auth;
  final Session session;

  @override
  State<_InviteSheet> createState() => _InviteSheetState();
}

class _InviteSheetState extends State<_InviteSheet> {
  Invite? _invite;
  String? _error;
  bool _copied = false;

  @override
  void initState() {
    super.initState();
    unawaited(_mint());
  }

  Future<void> _mint() async {
    try {
      final invite = await widget.auth.createInvite(
        accessToken: widget.session.accessToken,
        householdId: widget.session.householdId,
      );
      if (!mounted) {
        return;
      }
      setState(() => _invite = invite);
    } on AuthException catch (error) {
      if (!mounted) {
        return;
      }
      setState(
        () => _error = messageFor(AppLocalizations.of(context), error.failure),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final invite = _invite;

    return Padding(
      padding: const EdgeInsets.all(24),
      child: SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(l10n.inviteCodeTitle, style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            if (_error != null)
              Text(
                _error!,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.error,
                ),
              )
            else if (invite == null)
              // The one honest wait in the app: this value does not exist yet
              // anywhere, so there is nothing local to show instead.
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 16),
                child: LinearProgressIndicator(),
              )
            else ...[
              Text(l10n.inviteCodeExplain, style: theme.textTheme.bodyMedium),
              const SizedBox(height: 16),
              SelectableText(
                invite.code,
                style: theme.textTheme.headlineSmall?.copyWith(
                  fontFamily: 'monospace',
                  letterSpacing: 3,
                ),
              ),
              const SizedBox(height: 8),
              TextButton.icon(
                onPressed: () => unawaited(_copy(invite.code)),
                icon: const Icon(Icons.copy_outlined),
                label: Text(_copied ? l10n.codeCopied : l10n.copyCode),
              ),
            ],
          ],
        ),
      ),
    );
  }

  Future<void> _copy(String code) async {
    await Clipboard.setData(ClipboardData(text: code));
    if (!mounted) {
      return;
    }
    setState(() => _copied = true);
  }
}
