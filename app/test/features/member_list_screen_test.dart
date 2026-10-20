import 'dart:convert';

import 'package:choreo/core/api_client.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/features/members/member_list_screen.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:drift/drift.dart' show Value;
import 'package:drift/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../support/fake_auth.dart';
import '../support/screen.dart';

const _household = 'home';
const _me = 'member-me';

Session _session({String role = 'admin'}) {
  return Session(
    accountId: 'account-1',
    deviceId: 'device-1',
    accessToken: 'access-1',
    householdId: _household,
    memberId: _me,
    role: role,
    householdName: 'Maison',
  );
}

ApiClient _offlineApi() {
  return ApiClient(
    httpClient: MockClient((_) async => throw http.ClientException('offline')),
    baseUrl: 'http://example.invalid',
  );
}

void main() {
  late AppDatabase db;

  setUp(() => db = AppDatabase(NativeDatabase.memory()));
  tearDown(() => db.close());

  Future<void> addMember(
    String id, {
    String? name,
    String role = 'adult',
    String householdId = _household,
    int rev = 1,
  }) {
    return db
        .into(db.members)
        .insert(
          MembersCompanion.insert(
            id: id,
            householdId: householdId,
            rev: rev,
            role: Value(role),
            fields: Value(jsonEncode({'display_name': ?name})),
          ),
        );
  }

  Future<void> show(WidgetTester tester, {String role = 'admin'}) async {
    final auth = agreeableAuth();

    await tester.pumpWidget(
      MaterialApp(
        locale: const Locale('fr'),
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: MemberListScreen(
          db: db,
          session: _session(role: role),
          auth: auth,
          sessions: SessionRepository(db: db, auth: auth),
          api: _offlineApi(),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  screenTest('the household is listed with names and roles', (tester) async {
    await addMember(_me, name: 'Yahya', role: 'admin');
    await addMember('member-other', name: 'Sara', rev: 2);

    await show(tester);

    expect(find.text('Yahya'), findsOneWidget);
    expect(find.text('Admin'), findsOneWidget);
    expect(find.text('Sara'), findsOneWidget);
    expect(find.text('Adulte'), findsOneWidget);
  });

  // A member row can arrive before the page that carries its name: pages come
  // in revision order, not in causal order.
  screenTest('a member with no name yet is still shown', (tester) async {
    await addMember('member-nameless');

    await show(tester);

    expect(find.text('member-nameless'), findsOneWidget);
  });

  // A role a newer server knows and this app does not must not break the list.
  screenTest('an unknown role does not break the list', (tester) async {
    await addMember('member-future', name: 'Futur', role: 'co-parent');

    await show(tester);

    expect(find.text('Futur'), findsOneWidget);
  });

  screenTest('another household stays out of the list', (tester) async {
    await addMember('member-theirs', name: 'Ailleurs', householdId: 'other');

    await show(tester);

    expect(find.text('Ailleurs'), findsNothing);
  });

  // Inviting is an admin's gesture. Showing the button to a guest and having
  // the server refuse it afterwards would be our mistake, not theirs.
  screenTest('only an admin is offered the invite', (tester) async {
    await addMember(_me, name: 'Yahya');

    await show(tester, role: 'adult');

    expect(find.text('Inviter'), findsNothing);
  });

  screenTest('an admin gets a code to pass on', (tester) async {
    await addMember(_me, name: 'Yahya', role: 'admin');

    await show(tester);
    await tester.tap(find.text('Inviter'));
    await tester.pumpAndSettle();

    expect(find.text('K7MN4PQR8S'), findsOneWidget);
  });
}
