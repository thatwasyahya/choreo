import 'dart:async';
import 'dart:convert';

import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/features/check/check_screen.dart';
import 'package:choreo/features/members/invite_sheet.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:drift/drift.dart' show OrderingTerm;
import 'package:flutter/material.dart';

/// The two things on this screen that are not about members.
enum _Rare { check, signOut }

/// Who is in the household.
///
/// Read from the local replica like everything else, which is why it is legible
/// on a train: members arrive with the rest of the household's rows and nothing
/// here asks the network for them.
class MemberListScreen extends StatefulWidget {
  const MemberListScreen({
    required this.db,
    required this.session,
    required this.auth,
    required this.sessions,
    required this.api,
    super.key,
  });

  final AppDatabase db;
  final Session session;
  final AuthApi auth;
  final SessionRepository sessions;
  final ApiClient api;

  @override
  State<MemberListScreen> createState() => _MemberListScreenState();
}

class _MemberListScreenState extends State<MemberListScreen> {
  /// Subscribed once, like every other screen's stream here.
  late final Stream<List<Member>> _members = _watch();

  Session get _session => widget.session;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.membersTitle),
        // One button rather than two. A heading and a pair of actions do not
        // fit across a phone: the title came back as "Les memb…", which a
        // picture showed and a reading of the code never would. These two are
        // also the rarest things on the screen — a diagnostic, and leaving —
        // so a menu is where they belong.
        actions: [
          PopupMenuButton<_Rare>(
            icon: const Icon(Icons.more_horiz),
            position: PopupMenuPosition.under,
            onSelected: (choice) {
              switch (choice) {
                case _Rare.check:
                  unawaited(_showCheck(context));
                case _Rare.signOut:
                  unawaited(_signOut(context));
              }
            },
            itemBuilder: (context) => [
              PopupMenuItem(
                value: _Rare.check,
                child: Text(l10n.checkTitle),
              ),
              PopupMenuItem(
                value: _Rare.signOut,
                child: Text(l10n.signOut),
              ),
            ],
          ),
        ],
      ),
      body: StreamBuilder<List<Member>>(
        stream: _members,
        builder: (context, snapshot) {
          final members = snapshot.data ?? const <Member>[];

          return ListView.builder(
            itemCount: members.length,
            itemBuilder: (context, index) {
              final member = members[index];
              return ListTile(
                title: Text(_nameOf(member)),
                subtitle: Text(_roleOf(l10n, member.role)),
                trailing: member.id == _session.memberId
                    ? const Icon(Icons.person_outline)
                    : null,
              );
            },
          );
        },
      ),
      // Inviting is an admin's gesture. A guest seeing the button and being
      // refused by the server afterwards would be our mistake, not theirs.
      floatingActionButton: _session.role == 'admin'
          ? FloatingActionButton.extended(
              onPressed: () => unawaited(
                showInviteSheet(
                  context,
                  auth: widget.auth,
                  session: _session,
                ),
              ),
              icon: const Icon(Icons.person_add_outlined),
              label: Text(l10n.inviteMember),
            )
          : null,
    );
  }

  /// Leave the screens that assume a household BEFORE forgetting the tokens.
  ///
  /// The shell switches on the local database underneath, but a pushed route
  /// stays on top of whatever the shell is showing: without this, somebody who
  /// signs out keeps staring at a member list that no longer means anything.
  Future<void> _signOut(BuildContext context) async {
    Navigator.of(context).popUntil((route) => route.isFirst);
    await widget.sessions.signOut();
  }

  Future<void> _showCheck(BuildContext context) {
    return Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => CheckScreen(database: widget.db, api: widget.api),
      ),
    );
  }

  Stream<List<Member>> _watch() {
    final db = widget.db;

    return (db.select(db.members)
          ..where((m) => m.householdId.equals(_session.householdId))
          ..where((m) => m.deletedRev.isNull())
          ..orderBy([(m) => OrderingTerm.asc(m.rev)]))
        .watch();
  }

  /// A member's display name lives in the `fields` JSON, where provenance is
  /// tracked per field. Absent is normal: a member row can arrive before the
  /// page carrying its name.
  static String _nameOf(Member member) {
    final fields = jsonDecode(member.fields) as Map<String, dynamic>;
    final name = fields['display_name'] as String? ?? '';
    return name.isEmpty ? member.id : name;
  }

  static String _roleOf(AppLocalizations l10n, String role) {
    switch (role) {
      case 'admin':
        return l10n.roleAdmin;
      case 'child':
        return l10n.roleChild;
      case 'guest':
        return l10n.roleGuest;
      default:
        // Including a role a newer server knows and this app does not.
        return l10n.roleAdult;
    }
  }
}
