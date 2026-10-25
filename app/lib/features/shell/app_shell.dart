import 'dart:async';

import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/core/notice_repository.dart';
import 'package:choreo/core/series_repository.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/core/task_repository.dart';
import 'package:choreo/features/members/member_list_screen.dart';
import 'package:choreo/features/onboarding/household_setup_screen.dart';
import 'package:choreo/features/onboarding/welcome_screen.dart';
import 'package:choreo/features/tasks/repeat_screen.dart';
import 'package:choreo/features/tasks/task_list_screen.dart';
import 'package:flutter/material.dart';

/// What the app shows, decided by the local database alone.
///
/// Three states, read from one query: the way in, the household to found or
/// join, and the chores. No network call takes part in that decision, so a
/// phone that has been signed in once opens on its task list in aeroplane mode
/// — which is the whole promise of the product in one widget.
class AppShell extends StatefulWidget {
  const AppShell({
    required this.db,
    required this.sessions,
    required this.auth,
    required this.api,
    super.key,
  });

  final AppDatabase db;
  final SessionRepository sessions;
  final AuthApi auth;

  /// Only for the diagnostic screen, which is the one place in the app allowed
  /// to ask the server whether it is there.
  final ApiClient api;

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  /// The repositories, kept across rebuilds.
  ///
  /// Built fresh each time, they would hand the list a new Drift stream on
  /// every write to the identity row — a renewed token would blank the chores
  /// for a frame. They are cheap to hold and the household they belong to is
  /// the only thing that can invalidate them.
  String? _for;
  late TaskRepository _tasks;
  late NoticeRepository _notices;

  /// Subscribed once. Rebuilt in `build`, the query would be re-run on every
  /// frame that touches it.
  late final Stream<SessionState> _state = widget.sessions.watchState();

  @override
  Widget build(BuildContext context) {
    return StreamBuilder<SessionState>(
      stream: _state,
      builder: (context, snapshot) {
        final state = snapshot.data;
        if (state == null) {
          // One frame, before SQLite has answered. Deliberately blank: a
          // splash screen here would be a wait we do not actually have.
          return const SizedBox.shrink();
        }

        switch (state.stage) {
          case SessionStage.wayIn:
            return WelcomeScreen(sessions: widget.sessions);

          case SessionStage.household:
            return HouseholdSetupScreen(sessions: widget.sessions);

          case SessionStage.ready:
            final session = state.session!;
            _prepare(session);
            return TaskListScreen(
              repository: _tasks,
              notices: _notices,
              onShowMembers: () => unawaited(_showMembers(context, session)),
              onRepeat: (title) =>
                  unawaited(_showRepeat(context, session, title)),
            );
        }
      },
    );
  }

  void _prepare(Session session) {
    final key = '${session.householdId}/${session.memberId}';
    if (_for == key) {
      return;
    }

    _for = key;
    _tasks = TaskRepository(
      db: widget.db,
      householdId: session.householdId,
      memberId: session.memberId,
    );
    _notices = NoticeRepository(
      db: widget.db,
      householdId: session.householdId,
      memberId: session.memberId,
    );
  }

  Future<void> _showRepeat(
    BuildContext context,
    Session session,
    String title,
  ) {
    return Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => RepeatScreen(
          db: widget.db,
          title: title,
          series: SeriesRepository(
            db: widget.db,
            householdId: session.householdId,
            memberId: session.memberId,
          ),
        ),
      ),
    );
  }

  Future<void> _showMembers(BuildContext context, Session session) {
    return Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => MemberListScreen(
          db: widget.db,
          session: session,
          auth: widget.auth,
          sessions: widget.sessions,
          api: widget.api,
        ),
      ),
    );
  }
}
