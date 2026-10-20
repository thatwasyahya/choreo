import 'dart:async';
import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/notice_repository.dart';
import 'package:choreo/core/task_repository.dart';
import 'package:choreo/features/tasks/new_task_sheet.dart';
import 'package:choreo/features/tasks/review_tray.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// Everything the household has to do.
///
/// It watches the local database and nothing else. The sync engine writes to
/// SQLite, this screen catches up on its own, and there is no network call
/// anywhere in it — which is why it works identically in a supermarket, a
/// cellar and an aeroplane.
class TaskListScreen extends StatefulWidget {
  const TaskListScreen({
    required this.repository,
    required this.notices,
    this.onShowMembers,
    super.key,
  });

  final TaskRepository repository;
  final NoticeRepository notices;

  /// How to reach the member list, when there is one to reach. Passed in
  /// rather than built here: this screen reads the local database and knows
  /// nothing about accounts, and it stays testable without one.
  final VoidCallback? onShowMembers;

  @override
  State<TaskListScreen> createState() => _TaskListScreenState();
}

class _TaskListScreenState extends State<TaskListScreen> {
  /// Subscribed once, for the life of the screen.
  ///
  /// Built inside `build` instead, every rebuild would cancel the subscription
  /// and open a new one: a query re-run for nothing, and a frame with no data
  /// in a screen whose whole point is that the data is already here.
  late final Stream<List<Task>> _tasks = widget.repository.watchOpen();

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.tasksTitle),
        actions: [
          // Only ever present when there is something to review, so the list
          // does not carry a standing reminder that something might be wrong.
          ReviewTrayButton(notices: widget.notices),
          if (widget.onShowMembers != null)
            IconButton(
              onPressed: widget.onShowMembers,
              tooltip: l10n.membersTitle,
              icon: const Icon(Icons.people_outline),
            ),
        ],
      ),
      body: StreamBuilder<List<Task>>(
        stream: _tasks,
        builder: (context, snapshot) {
          final tasks = snapshot.data;
          if (tasks == null) {
            // The very first frame, before SQLite has answered. Deliberately
            // blank rather than a spinner: a spinner on a local read teaches
            // people that this app waits for things, and it does not.
            return const SizedBox.shrink();
          }
          if (tasks.isEmpty) {
            return _Empty(message: l10n.emptyTasks);
          }

          return ListView.builder(
            // Room under the last row so the floating button never covers it.
            padding: const EdgeInsets.only(bottom: 88),
            itemCount: tasks.length,
            itemBuilder: (context, index) {
              return _TaskRow(
                task: tasks[index],
                repository: widget.repository,
              );
            },
          );
        },
      ),
      // Bottom right, which is the thumb's reach on a phone held in one hand.
      // Principle 3 is a placement, not an intention.
      floatingActionButton: FloatingActionButton(
        onPressed: () => unawaited(_create(context)),
        tooltip: l10n.newTask,
        child: const Icon(Icons.add),
      ),
    );
  }

  Future<void> _create(BuildContext context) async {
    final title = await showNewTaskSheet(context);
    if (title == null || title.trim().isEmpty) {
      return;
    }
    await widget.repository.create(title.trim());
  }
}

class _Empty extends StatelessWidget {
  const _Empty({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Text(
          message,
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.bodyLarge,
        ),
      ),
    );
  }
}

class _TaskRow extends StatelessWidget {
  const _TaskRow({required this.task, required this.repository});

  final Task task;
  final TaskRepository repository;

  List<Map<String, dynamic>> get _completions {
    return (jsonDecode(task.completions) as List<dynamic>)
        .cast<Map<String, dynamic>>();
  }

  bool get _done => _completions.isNotEmpty;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    return Dismissible(
      key: ValueKey(task.id),
      direction: DismissDirection.endToStart,
      background: ColoredBox(
        color: theme.colorScheme.errorContainer,
        child: Align(
          alignment: Alignment.centerRight,
          child: Padding(
            padding: const EdgeInsets.only(right: 24),
            child: Icon(
              Icons.delete_outline,
              color: theme.colorScheme.onErrorContainer,
            ),
          ),
        ),
      ),
      onDismissed: (_) => unawaited(repository.remove(task)),
      child: CheckboxListTile(
        value: _done,
        onChanged: (_) => unawaited(_toggle()),
        // The label sits after the box so the whole row is one target, and the
        // box lands under the thumb rather than across the screen.
        controlAffinity: ListTileControlAffinity.leading,
        title: Text(
          task.title,
          style: _done
              ? theme.textTheme.bodyLarge?.copyWith(
                  decoration: TextDecoration.lineThrough,
                  color: theme.textTheme.bodySmall?.color,
                )
              : theme.textTheme.bodyLarge,
        ),
        subtitle: _done
            ? Text(l10n.doneBy(_names()), style: theme.textTheme.bodySmall)
            : null,
      ),
    );
  }

  Future<void> _toggle() {
    return _done ? repository.uncomplete(task) : repository.complete(task);
  }

  /// Who ticked it. An attribution and never a ranking: product principle 6
  /// forbids a scoreboard, and this is the line where one would appear first.
  String _names() {
    return _completions
        .map((c) => c['member_id'] as String? ?? '')
        .where((name) => name.isNotEmpty)
        .join(', ');
  }
}
