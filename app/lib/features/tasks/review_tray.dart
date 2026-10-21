import 'dart:async';
import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/notice_repository.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// « À revoir » — what the server could not do the way somebody asked.
///
/// Never an error dialog, and never in the way: a card waits here until the
/// person has a moment for it, and it carries their own text verbatim so that
/// "put my text back" is a real offer rather than an apology.
class ReviewTray extends StatefulWidget {
  const ReviewTray({required this.notices, super.key});

  final NoticeRepository notices;

  @override
  State<ReviewTray> createState() => _ReviewTrayState();
}

class _ReviewTrayState extends State<ReviewTray> {
  /// Subscribed once: rebuilt in `build`, the query would be cancelled and
  /// re-run on every frame that touches this screen.
  late final Stream<List<Notice>> _cards = widget.notices.watchOpen();

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      appBar: AppBar(title: Text(l10n.reviewTray)),
      body: StreamBuilder<List<Notice>>(
        stream: _cards,
        builder: (context, snapshot) {
          final cards = snapshot.data;
          if (cards == null) {
            return const SizedBox.shrink();
          }
          if (cards.isEmpty) {
            // The ordinary state, and worth saying plainly: a tray that only
            // ever shows trouble teaches people to dread opening it.
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(32),
                child: Text(l10n.reviewEmpty, textAlign: TextAlign.center),
              ),
            );
          }

          return ListView.builder(
            padding: const EdgeInsets.all(12),
            itemCount: cards.length,
            itemBuilder: (context, index) {
              return _NoticeCard(
                notice: cards[index],
                notices: widget.notices,
              );
            },
          );
        },
      ),
    );
  }
}

/// The button that opens the tray, with the number of cards waiting.
///
/// It disappears when there is nothing to review. A permanent badge at zero is
/// a standing invitation to worry.
class ReviewTrayButton extends StatefulWidget {
  const ReviewTrayButton({required this.notices, super.key});

  final NoticeRepository notices;

  @override
  State<ReviewTrayButton> createState() => _ReviewTrayButtonState();
}

class _ReviewTrayButtonState extends State<ReviewTrayButton> {
  late final Stream<int> _waiting = widget.notices.watchOpenCount();

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    return StreamBuilder<int>(
      stream: _waiting,
      builder: (context, snapshot) {
        final waiting = snapshot.data ?? 0;
        if (waiting == 0) {
          return const SizedBox.shrink();
        }

        return IconButton(
          tooltip: l10n.reviewPending(waiting),
          // Awaited inside an async closure rather than wrapped in
          // `unawaited`: a newer SDK annotates Navigator.push as not needing an
          // await and flags the wrapper, while this one flags the bare call.
          // Awaiting satisfies both, and the route's result is of no interest
          // either way.
          onPressed: () async {
            await Navigator.of(context).push(
              MaterialPageRoute<void>(
                builder: (_) => ReviewTray(notices: widget.notices),
              ),
            );
          },
          icon: Badge.count(
            count: waiting,
            child: const Icon(Icons.inbox_outlined),
          ),
        );
      },
    );
  }
}

class _NoticeCard extends StatelessWidget {
  const _NoticeCard({required this.notice, required this.notices});

  final Notice notice;
  final NoticeRepository notices;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    final mine = _summarise(notice.mine);
    final theirs = _summarise(notice.theirs);

    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(_sentence(l10n), style: theme.textTheme.titleMedium),
            if (mine != null) ...[
              const SizedBox(height: 12),
              _Quote(label: l10n.noticeMine, text: mine),
            ],
            if (theirs != null) ...[
              const SizedBox(height: 8),
              _Quote(label: l10n.noticeTheirs, text: theirs),
            ],
            const SizedBox(height: 8),
            // Three answers at most, and the first one is the generous one.
            Wrap(
              alignment: WrapAlignment.end,
              spacing: 8,
              children: [
                TextButton(
                  onPressed: () => unawaited(_answer(NoticeChoice.dismiss)),
                  child: Text(l10n.noticeDismiss),
                ),
                if (theirs != null)
                  TextButton(
                    onPressed: () => unawaited(_answer(NoticeChoice.theirs)),
                    child: Text(l10n.noticeKeepTheirs),
                  ),
                if (mine != null && notice.targetRowId != null)
                  FilledButton(
                    onPressed: () => unawaited(_answer(NoticeChoice.mine)),
                    child: Text(l10n.noticeKeepMine),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _answer(NoticeChoice choice) {
    return notices.resolve(notice, choice);
  }

  /// A localisation key, never a sentence off the wire.
  ///
  /// An unknown code still earns a card: a newer server may send one this
  /// version has never heard of, and silence would be worse than a vague line.
  String _sentence(AppLocalizations l10n) {
    switch (notice.code) {
      case 'field_superseded':
        return l10n.noticeFieldSuperseded;
      case 'target_gone':
        return l10n.noticeTargetGone;
      case 'edit_vs_delete':
        return l10n.noticeEditVsDelete;
      case 'stale_delete':
        return l10n.noticeStaleDelete;
      case 'successor_in_use':
        return l10n.noticeSuccessorInUse;
      default:
        return l10n.noticeUnknown;
    }
  }

  /// The values a person can read, as they were written.
  ///
  /// Field names are not shown: in Phase 1 a card carries a title or a note,
  /// and "title: Sortir les poubelles" reads like a bug report. A field this
  /// version cannot render is left out of the quote but still travels in the
  /// rewrite, which the repository sends whole.
  static String? _summarise(String? raw) {
    if (raw == null) {
      return null;
    }

    final fields = jsonDecode(raw) as Map<String, dynamic>;
    final text = fields.values
        .map((value) => value?.toString() ?? '')
        .where((value) => value.isNotEmpty)
        .join(' · ');

    return text.isEmpty ? null : text;
  }
}

class _Quote extends StatelessWidget {
  const _Quote({required this.label, required this.text});

  final String label;
  final String text;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(label, style: theme.textTheme.labelSmall),
        Text(text, style: theme.textTheme.bodyMedium),
      ],
    );
  }
}
