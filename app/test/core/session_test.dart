import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/database.dart';
import 'package:choreo/core/session.dart';
import 'package:drift/drift.dart' show Value;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import '../support/fake_auth.dart';

void main() {
  late AppDatabase db;

  /// Every request the fake server was asked, in order.
  final seen = <http.Request>[];

  AuthApi agreeable() => agreeableAuth(seen: seen);

  setUp(() {
    db = AppDatabase(NativeDatabase.memory());
    seen.clear();
  });
  tearDown(() => db.close());

  SessionRepository repoWith(AuthApi auth) {
    return SessionRepository(
      db: db,
      auth: auth,
      timeZone: 'Europe/Paris',
      wallClock: () => DateTime.utc(2026, 10, 19, 14, 30),
    );
  }

  Future<DeviceIdentityData?> identity() {
    return db.select(db.deviceIdentity).getSingleOrNull();
  }

  // --- the three states ------------------------------------------------------

  test('a fresh installation is at the way in', () async {
    final repo = repoWith(agreeable());

    expect((await repo.watchState().first).stage, SessionStage.wayIn);
  });

  // An account with no household is a real state, and not the same as having
  // signed out: somebody created one and is waiting for a code to be read out.
  test('an account with no household asks for one', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');

    expect((await repo.watchState().first).stage, SessionStage.household);
  });

  test('a household on screen is the ready state', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');
    await repo.createHousehold(name: 'Maison', displayName: 'Yahya');

    final state = await repo.watchState().first;
    expect(state.stage, SessionStage.ready);
    expect(state.session?.householdId, 'home');
    expect(state.session?.memberId, 'member-me');
    expect(state.session?.role, 'admin');
    expect(state.session?.householdName, 'Maison');
  });

  // --- signing up ------------------------------------------------------------

  test('signing up returns the code and keeps the credentials', () async {
    final repo = repoWith(agreeable());

    final code = await repo.signUp(login: 'yahya', password: 'un mot de passe');

    expect(code, 'K7MN-4PQR-8STV-2WXY-3Z5A-6BCD');

    final stored = await identity();
    expect(stored?.accountId, 'account-1');
    expect(stored?.deviceId, 'device-1');
    expect(stored?.accessToken, 'access-1');
    expect(stored?.refreshToken, 'refresh-1');
    expect(stored?.login, 'yahya');
  });

  // The recovery code is the only way back into an account. Keeping a copy on
  // the phone would make it worthless the day the phone is lost — which is the
  // day it exists for.
  test('the recovery code is not kept anywhere on the phone', () async {
    final repo = repoWith(agreeable());

    final code = await repo.signUp(login: 'yahya', password: 'un mot de passe');

    final stored = await identity();
    expect(stored?.accessToken, isNot(contains(code)));
    expect(stored?.refreshToken, isNot(contains(code)));
    expect(stored?.login, isNot(contains(code)));
  });

  test('the password is never stored', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');

    final row = (await identity())!.toJson().toString();
    expect(row, isNot(contains('un mot de passe')));
  });

  // --- signing in ------------------------------------------------------------

  // Presenting the device id is what lets a phone sign back in without losing
  // its cursor — and so without rebuilding its replica from a snapshot.
  test('signing in presents the device it already has', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');
    await repo.signIn(login: 'yahya', password: 'un mot de passe');

    final signIn = seen.lastWhere((r) => r.url.path == '/v1/sessions');
    expect(bodyOf(signIn)['device_id'], 'device-1');
  });

  test('a first sign-in sends no device id at all', () async {
    final repo = repoWith(agreeable());

    await repo.signIn(login: 'yahya', password: 'un mot de passe');

    final signIn = seen.firstWhere((r) => r.url.path == '/v1/sessions');
    expect(
      bodyOf(signIn).containsKey('device_id'),
      isFalse,
      reason: 'a null would ask the server to parse nothing',
    );
  });

  // Asked once, here, so that no screen ever has to.
  test('signing in brings back where this account belongs', () async {
    final repo = repoWith(agreeable());

    await repo.signIn(login: 'yahya', password: 'un mot de passe');

    final memberships = await db.select(db.memberships).get();
    expect(memberships.single.householdId, 'home');
    expect(memberships.single.memberId, 'member-me');
    expect(memberships.single.current, 1);
  });

  // --- households ------------------------------------------------------------

  // Somebody joining their parent's household from their own kitchen should not
  // find their own chores gone.
  test('a second household does not take over the screen', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');
    await repo.createHousehold(name: 'Maison', displayName: 'Yahya');
    await repo.join(code: 'K7MN4PQR8S', displayName: 'Yahya');

    final current = await repo.watchState().first;
    expect(current.session?.householdId, 'home');

    final joined = await (db.select(
      db.memberships,
    )..where((m) => m.householdId.equals('other-home'))).getSingle();
    expect(joined.current, 0);
  });

  test('switching household changes what is on screen', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');
    await repo.createHousehold(name: 'Maison', displayName: 'Yahya');
    await repo.join(code: 'K7MN4PQR8S', displayName: 'Yahya');

    await repo.show('other-home');

    final state = await repo.watchState().first;
    expect(state.session?.householdId, 'other-home');
    expect(state.session?.memberId, 'member-guest');
  });

  test('creating a household sends the phone time zone', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');
    await repo.createHousehold(name: 'Maison', displayName: 'Yahya');

    final created = seen.lastWhere((r) => r.url.path == '/v1/households');
    expect(bodyOf(created)['tz'], 'Europe/Paris');
    expect(bodyOf(created)['display_name'], 'Yahya');
  });

  // --- signing out and renewing ---------------------------------------------

  // Signing out is not being revoked. The rows stay: the same person signing
  // back in on the same device picks up where they were.
  test('signing out forgets the tokens and keeps the replica', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');
    await repo.createHousehold(name: 'Maison', displayName: 'Yahya');
    await db
        .into(db.tasks)
        .insert(
          TasksCompanion.insert(
            id: 'task-1',
            householdId: 'home',
            rev: 1,
            title: const Value('Sortir les poubelles'),
          ),
        );

    await repo.signOut();

    expect((await repo.watchState().first).stage, SessionStage.wayIn);

    final stored = await identity();
    expect(stored?.accessToken, isNull);
    expect(stored?.refreshToken, isNull);
    expect(
      stored?.deviceId,
      'device-1',
      reason: 'the device survives, or signing back in costs a whole snapshot',
    );
    expect(await db.countTasks('home'), 1);
    expect(await db.select(db.memberships).get(), hasLength(1));
  });

  test('renewing keeps the device and replaces the pair', () async {
    final repo = repoWith(agreeable());

    await repo.signUp(login: 'yahya', password: 'un mot de passe');
    await repo.renew();

    final stored = await identity();
    expect(stored?.accessToken, 'access-3');
    expect(stored?.refreshToken, 'refresh-3');
    expect(stored?.deviceId, 'device-1');
  });

  test('renewing with nothing to renew says so rather than hanging', () async {
    final repo = repoWith(agreeable());

    await expectLater(
      repo.renew(),
      throwsA(
        isA<AuthException>().having(
          (e) => e.failure,
          'failure',
          AuthFailure.badCredentials,
        ),
      ),
    );
  });

  // --- what the server refuses ----------------------------------------------

  test('a wrong password is a wrong password, and not which', () async {
    final repo = repoWith(refusingAuth(401, 'invalid_credentials'));

    await expectLater(
      repo.signIn(login: 'yahya', password: 'faux'),
      throwsA(
        isA<AuthException>().having(
          (e) => e.failure,
          'failure',
          AuthFailure.badCredentials,
        ),
      ),
    );
    expect((await repo.watchState().first).stage, SessionStage.wayIn);
  });

  test('a taken login is said plainly', () async {
    final repo = repoWith(refusingAuth(409, 'login_taken'));

    await expectLater(
      repo.signUp(login: 'yahya', password: 'un mot de passe'),
      throwsA(
        isA<AuthException>().having(
          (e) => e.failure,
          'failure',
          AuthFailure.loginTaken,
        ),
      ),
    );
  });

  // Unknown, used up or expired, and deliberately never which: telling them
  // apart would turn the endpoint into a code oracle.
  test('an invite that does not work says only that', () async {
    final repo = repoWith(refusingAuth(404, 'invite_unknown'));
    // Signed in already: redeeming a code is something only an account can do.
    await db
        .into(db.deviceIdentity)
        .insert(
          const DeviceIdentityCompanion(
            id: Value(1),
            deviceId: Value('device-1'),
            accessToken: Value('access-1'),
            refreshToken: Value('refresh-1'),
          ),
        );

    await expectLater(
      repo.join(code: 'K7MN4PQR8S', displayName: 'Yahya'),
      throwsA(
        isA<AuthException>().having(
          (e) => e.failure,
          'failure',
          AuthFailure.inviteUnknown,
        ),
      ),
    );
  });

  // A code this version has never heard of becomes "something went wrong"
  // rather than a guess at what a newer server meant.
  test('an unknown code is not interpreted', () async {
    final repo = repoWith(refusingAuth(418, 'a_code_from_the_future'));

    await expectLater(
      repo.signIn(login: 'yahya', password: 'un mot de passe'),
      throwsA(
        isA<AuthException>().having(
          (e) => e.failure,
          'failure',
          AuthFailure.server,
        ),
      ),
    );
  });

  test('an unreachable server is offline, not broken', () async {
    final repo = repoWith(unreachableAuth());

    await expectLater(
      repo.signIn(login: 'yahya', password: 'un mot de passe'),
      throwsA(
        isA<AuthException>().having(
          (e) => e.failure,
          'failure',
          AuthFailure.offline,
        ),
      ),
    );
  });
}
