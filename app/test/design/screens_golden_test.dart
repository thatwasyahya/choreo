import 'dart:convert';

import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/core/notice_repository.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/core/task_repository.dart';
import 'package:choreo/design/theme.dart';
import 'package:choreo/features/members/member_list_screen.dart';
import 'package:choreo/features/onboarding/credentials_screen.dart';
import 'package:choreo/features/onboarding/household_setup_screen.dart';
import 'package:choreo/features/onboarding/recovery_code_screen.dart';
import 'package:choreo/features/onboarding/welcome_screen.dart';
import 'package:choreo/features/tasks/review_tray.dart';
import 'package:choreo/features/tasks/task_list_screen.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:drift/drift.dart' show Value;
import 'package:drift/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../support/fake_auth.dart';
import '../support/fonts.dart';

const _household = 'home';
const _me = 'member-me';
const _other = 'member-other';

/// A phone, roughly: a 2026 mid-range Android in logical pixels.
const Size _phone = Size(390, 844);

/// Pictures of every screen, so the design can be LOOKED at.
///
/// A design reviewed by reading its code is a design reviewed by its author's
/// intentions. These render each screen at phone size with the real typefaces
/// loaded, which makes the output something a person can judge: the type
/// hierarchy, the air, whether the accent lands where the eye should go.
///
/// They double as regression protection — a token changed by accident shows up
/// as a diff in nine images — but the first job is to be seen.
void main() {
  late AppDatabase db;

  setUpAll(loadChoreoFonts);

  setUp(() => db = AppDatabase(NativeDatabase.memory()));
  tearDown(() => db.close());

  Future<void> shoot(
    WidgetTester tester,
    String name,
    Widget screen, {
    Brightness brightness = Brightness.light,
    Future<void> Function(WidgetTester tester)? before,
  }) async {
    tester.view
      ..physicalSize = _phone
      ..devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    await tester.pumpWidget(
      MaterialApp(
        locale: const Locale('fr'),
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        // Off: the debug ribbon is drawn over the corner of every image, and
        // a design reviewed through a red banner is a design reviewed badly.
        debugShowCheckedModeBanner: false,
        theme: brightness == Brightness.light
            ? ChoreoTheme.light
            : ChoreoTheme.dark,
        home: screen,
      ),
    );
    await tester.pumpAndSettle();

    if (before != null) {
      await before(tester);
    }

    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/$name.png'),
    );

    // Unmount before the database closes. A Drift subscription still attached
    // to a closing database hangs the test until the ten-minute timeout, with
    // the image already written — which looks like a design problem and is a
    // harness problem.
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(milliseconds: 1));
  }

  TaskRepository tasksFor() => TaskRepository(
    db: db,
    householdId: _household,
    memberId: _me,
    elapsed: () => 0,
    wallClock: () => DateTime.utc(2026, 10, 24, 9),
  );

  NoticeRepository noticesFor() => NoticeRepository(
    db: db,
    householdId: _household,
    memberId: _me,
    elapsed: () => 0,
    wallClock: () => DateTime.utc(2026, 10, 24, 9),
  );

  SessionRepository sessionsFor() => SessionRepository(
    db: db,
    auth: agreeableAuth(),
    timeZone: () async => 'Europe/Paris',
  );

  Session session({String role = 'admin'}) => Session(
    accountId: 'account-1',
    deviceId: 'device-1',
    accessToken: 'access-1',
    householdId: _household,
    memberId: _me,
    role: role,
    householdName: 'Maison',
  );

  Future<void> seedTasks() async {
    final repo = tasksFor();
    final shopping = await repo.create('Acheter du pain et du lait');
    await repo.create('Appeler le plombier pour le chauffe-eau');
    await repo.create('Sortir les poubelles');
    final done = await repo.create('Arroser les plantes');

    // One ticked, by somebody else, so the "done by" line is in the picture.
    await (db.update(db.tasks)..where((t) => t.id.equals(done))).write(
      TasksCompanion(
        completions: Value(
          jsonEncode([
            {'member_id': 'Sara', 'completion_id': 'c-1'},
          ]),
        ),
      ),
    );
    // And one with a longer title, because real titles wrap.
    await (db.update(db.tasks)..where((t) => t.id.equals(shopping))).write(
      const TasksCompanion(
        title: Value('Acheter du pain, du lait et des œufs'),
      ),
    );
  }

  Future<void> seedMembers() async {
    for (final member in [
      (id: _me, name: 'Yahya', role: 'admin', rev: 1),
      (id: _other, name: 'Sara', role: 'adult', rev: 2),
      (id: 'member-kid', name: 'Nour', role: 'child', rev: 3),
    ]) {
      await db.into(db.members).insert(
        MembersCompanion.insert(
          id: member.id,
          householdId: _household,
          rev: member.rev,
          role: Value(member.role),
          fields: Value(jsonEncode({'display_name': member.name})),
        ),
      );
    }
  }

  Future<void> seedNotice() async {
    await db.into(db.notices).insert(
      NoticesCompanion.insert(
        id: 'notice-1',
        householdId: _household,
        rev: 10,
        forMember: const Value(_me),
        kind: const Value('decision'),
        code: const Value('edit_vs_delete'),
        targetEntity: const Value('task'),
        targetRowId: const Value('task-1'),
        mine: Value(jsonEncode({'title': 'Appeler le plombier (devis)'})),
        theirs: Value(jsonEncode({'title': 'Appeler le plombier'})),
      ),
    );
  }

  // --- the way in ------------------------------------------------------------

  testWidgets('welcome', (tester) async {
    await shoot(tester, 'welcome', WelcomeScreen(sessions: sessionsFor()));
  });

  testWidgets('welcome, at night', (tester) async {
    await shoot(
      tester,
      'welcome-dark',
      WelcomeScreen(sessions: sessionsFor()),
      brightness: Brightness.dark,
    );
  });

  testWidgets('signing up', (tester) async {
    await shoot(
      tester,
      'credentials',
      CredentialsScreen(sessions: sessionsFor(), mode: CredentialsMode.signUp),
    );
  });

  testWidgets('the recovery code', (tester) async {
    await shoot(
      tester,
      'recovery-code',
      const RecoveryCodeScreen(code: 'K7MN-4PQR-8STV-2WXY-3Z5A-6BCD'),
    );
  });

  testWidgets('founding a household', (tester) async {
    await shoot(
      tester,
      'household-setup',
      HouseholdSetupScreen(sessions: sessionsFor()),
    );
  });

  // --- the chores ------------------------------------------------------------

  testWidgets('the task list', (tester) async {
    await seedTasks();
    await shoot(
      tester,
      'tasks',
      TaskListScreen(
        repository: tasksFor(),
        notices: noticesFor(),
        onShowMembers: () {},
      ),
    );
  });

  testWidgets('the task list, at night', (tester) async {
    await seedTasks();
    await shoot(
      tester,
      'tasks-dark',
      TaskListScreen(
        repository: tasksFor(),
        notices: noticesFor(),
        onShowMembers: () {},
      ),
      brightness: Brightness.dark,
    );
  });

  // The state people see most often at first, and the one where a product
  // reveals its manners. Principle 6: no reproach, no injunction.
  testWidgets('nothing to do', (tester) async {
    await shoot(
      tester,
      'tasks-empty',
      TaskListScreen(repository: tasksFor(), notices: noticesFor()),
    );
  });

  // The ten-second screen: one field, already at the keyboard. Worth a picture
  // of its own, because its whole argument is what is NOT on it.
  testWidgets('the create sheet', (tester) async {
    await seedTasks();
    await shoot(
      tester,
      'tasks-create',
      TaskListScreen(repository: tasksFor(), notices: noticesFor()),
      // The sheet is opened before the picture, so the tree is still mounted.
      before: (tester) async {
        await tester.tap(find.byType(FloatingActionButton));
        await tester.pumpAndSettle();
      },
    );
  });

  // --- the rest --------------------------------------------------------------

  testWidgets('the review tray', (tester) async {
    await seedNotice();
    await shoot(tester, 'review-tray', ReviewTray(notices: noticesFor()));
  });

  testWidgets('the members', (tester) async {
    await seedMembers();
    await shoot(
      tester,
      'members',
      MemberListScreen(
        db: db,
        session: session(),
        auth: agreeableAuth(),
        sessions: sessionsFor(),
        api: ApiClient(
          httpClient: MockClient(
            (_) async => throw http.ClientException('offline'),
          ),
          baseUrl: 'http://example.invalid',
        ),
      ),
    );
  });
}
