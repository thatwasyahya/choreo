import 'dart:async';

import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// The phase 0 verification screen.
///
/// It exists to prove one thing: the app reads its own database, and talks to
/// the server when it happens to be reachable. It is not a product screen and
/// gets replaced in phase 1.
class CheckScreen extends StatefulWidget {
  const CheckScreen({required this.database, required this.api, super.key});

  final AppDatabase database;
  final ApiClient api;

  @override
  State<CheckScreen> createState() => _CheckScreenState();
}

class _CheckScreenState extends State<CheckScreen> {
  ServerHealth? _health;
  int _observations = 0;
  DateTime? _latest;
  bool _busy = true;

  @override
  void initState() {
    super.initState();
    unawaited(_check());
  }

  Future<void> _check() async {
    setState(() => _busy = true);

    final health = await widget.api.health();

    // Written locally first, always — including when the server answered.
    // The phone's database is the one that has to be right.
    await widget.database.record(
      health.isReachable ? 'reachable' : 'unreachable',
    );

    final observations = await widget.database.countObservations();
    final latest = await widget.database.latestObservation();

    if (!mounted) {
      return;
    }
    setState(() {
      _health = health;
      _observations = observations;
      _latest = latest?.observedAt;
      _busy = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final latest = _latest;

    return Scaffold(
      appBar: AppBar(title: Text(l10n.checkTitle)),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _Section(
            title: l10n.serverSection,
            child: Text(_serverLine(l10n), style: theme.textTheme.bodyLarge),
          ),
          const SizedBox(height: 24),
          _Section(
            title: l10n.localSection,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  l10n.observationsRecorded(_observations),
                  style: theme.textTheme.bodyLarge,
                ),
                if (latest != null)
                  Text(
                    l10n.lastObservation(latest),
                    style: theme.textTheme.bodyMedium,
                  ),
              ],
            ),
          ),
          const SizedBox(height: 24),
          Text(l10n.offlineNote, style: theme.textTheme.bodySmall),
          const SizedBox(height: 24),
          FilledButton(
            onPressed: _busy ? null : () => unawaited(_check()),
            child: Text(l10n.retry),
          ),
        ],
      ),
    );
  }

  String _serverLine(AppLocalizations l10n) {
    if (_busy) {
      return l10n.serverChecking;
    }
    final health = _health;
    if (health == null || !health.isReachable) {
      return l10n.serverUnreachable;
    }
    return l10n.serverReachable(health.version ?? '?');
  }
}

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title.toUpperCase(),
          style: theme.textTheme.labelSmall?.copyWith(
            color: theme.colorScheme.primary,
            letterSpacing: 1.2,
          ),
        ),
        const SizedBox(height: 4),
        child,
      ],
    );
  }
}
