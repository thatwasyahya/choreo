import 'dart:async';

import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// The verification screen, until the real ones arrive in lot L1.9.
///
/// It reports what the phone itself holds — tasks, the outbox, rows set aside —
/// and what the server said if it happened to answer. Everything on it is read
/// from the local database, so it is also the smallest honest demonstration
/// that the app does not need the network to be useful.
class CheckScreen extends StatefulWidget {
  const CheckScreen({
    required this.database,
    required this.api,
    this.householdId = '',
    super.key,
  });

  final AppDatabase database;
  final ApiClient api;

  /// Empty until a household has been joined, which is lot L1.9.
  final String householdId;

  @override
  State<CheckScreen> createState() => _CheckScreenState();
}

class _CheckScreenState extends State<CheckScreen> {
  ServerHealth? _health;
  int _tasks = 0;
  int _pending = 0;
  int _quarantined = 0;
  bool _busy = true;

  @override
  void initState() {
    super.initState();
    unawaited(_check());
  }

  Future<void> _check() async {
    setState(() => _busy = true);

    // The local counts first, and without waiting for the network: nothing on
    // this screen is allowed to depend on the server answering.
    final tasks = await widget.database.countTasks(widget.householdId);
    final pending = await widget.database.pendingCount();
    final quarantined = await widget.database.quarantinedCount();

    final health = await widget.api.health();

    if (!mounted) {
      return;
    }
    setState(() {
      _health = health;
      _tasks = tasks;
      _pending = pending;
      _quarantined = quarantined;
      _busy = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

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
                  l10n.tasksStored(_tasks),
                  style: theme.textTheme.bodyLarge,
                ),
                Text(
                  l10n.pendingToSend(_pending),
                  style: theme.textTheme.bodyMedium,
                ),
                // Shown only when there is something to say. Rows set aside
                // never block synchronisation, so a permanent zero would be
                // noise.
                if (_quarantined > 0)
                  Text(
                    l10n.quarantinedRows(_quarantined),
                    style: theme.textTheme.bodyMedium?.copyWith(
                      color: theme.colorScheme.error,
                    ),
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
