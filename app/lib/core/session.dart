import 'package:choreo/core/auth_api.dart';
import 'package:choreo/core/database.dart';
import 'package:drift/drift.dart';

/// Who this installation is, and which household it is looking at.
///
/// Read from the phone's own database, which is what lets the app open on the
/// task list with no network: a session is a local fact, and the server only
/// ever confirms it.
class Session {
  const Session({
    required this.accountId,
    required this.deviceId,
    required this.accessToken,
    required this.householdId,
    required this.memberId,
    required this.role,
    this.householdName,
    this.login,
  });

  final String accountId;
  final String deviceId;
  final String accessToken;

  final String householdId;
  final String memberId;
  final String role;
  final String? householdName;
  final String? login;
}

/// Where the app is in getting somebody to their chores.
enum SessionStage {
  /// Nobody has signed in on this installation, or somebody signed out.
  wayIn,

  /// An account, but no household yet. Created one and stopped halfway, or
  /// waiting for a code from somebody else.
  household,

  /// A household on screen, and who we are in it.
  ready,
}

/// The one thing the app shell watches.
class SessionState {
  const SessionState(this.stage, [this.session]);

  final SessionStage stage;

  /// Present exactly when the stage is [SessionStage.ready].
  final Session? session;
}

/// Signing up, signing in, and belonging to a household.
///
/// The only part of the app that cannot work offline, and the comment is worth
/// making because everything else can: there is no way to prove who you are to
/// a server that has never met you. Once it has, nothing on any screen waits
/// for the network again.
class SessionRepository {
  SessionRepository({
    required this.db,
    required this.auth,
    DateTime Function()? wallClock,
    String? timeZone,
  }) : _wallClock = wallClock ?? DateTime.now,
       _timeZone = timeZone ?? DateTime.now().timeZoneName;

  final AppDatabase db;
  final AuthApi auth;

  final DateTime Function() _wallClock;
  final String _timeZone;

  /// Where the app is, as a live stream off the local database.
  ///
  /// One query, and it is the only thing the shell watches: an installation
  /// with credentials and no household is a real state — somebody created an
  /// account and is waiting for a code to be read out to them — and it is not
  /// the same as having signed out.
  Stream<SessionState> watchState() {
    final query = db.select(db.deviceIdentity).join([
      leftOuterJoin(db.memberships, db.memberships.current.equals(1)),
    ]);

    return query.watchSingleOrNull().map((row) {
      final identity = row?.readTableOrNull(db.deviceIdentity);
      final token = identity?.accessToken ?? '';
      if (identity == null || token.isEmpty) {
        return const SessionState(SessionStage.wayIn);
      }

      final membership = row?.readTableOrNull(db.memberships);
      if (membership == null) {
        return const SessionState(SessionStage.household);
      }

      return SessionState(
        SessionStage.ready,
        _session(identity, membership),
      );
    });
  }

  /// The household on screen, and who we are in it.
  Stream<Session?> watch() {
    return watchState().map((state) => state.session);
  }

  Future<Session?> current() => watch().first;

  /// Whether this installation has credentials, household or not.
  Future<bool> hasAccount() async {
    final identity = await db.select(db.deviceIdentity).getSingleOrNull();
    return (identity?.accessToken ?? '').isNotEmpty;
  }

  /// Create an account, and come back with the code to write down.
  ///
  /// The recovery code is returned rather than stored: the server keeps only a
  /// hash and can never show it again, and with no e-mail address in this
  /// product it is the only way back in. Keeping a copy on the phone would
  /// make it worthless the day the phone is lost — which is the day it is for.
  Future<String> signUp({
    required String login,
    required String password,
  }) async {
    final account = await auth.createAccount(login: login, password: password);

    await _remember(
      accountId: account.accountId,
      deviceId: account.deviceId,
      login: login,
      tokens: account.tokens,
    );

    return account.recoveryCode;
  }

  /// Sign in, presenting the device id this installation already has.
  ///
  /// Presenting it is what lets a phone sign back in without losing its
  /// cursor, and so without rebuilding its replica from a snapshot.
  Future<void> signIn({
    required String login,
    required String password,
  }) async {
    final known = await db.select(db.deviceIdentity).getSingleOrNull();

    final signedIn = await auth.signIn(
      login: login,
      password: password,
      deviceId: known?.deviceId,
    );

    await _remember(
      accountId: signedIn.accountId,
      deviceId: signedIn.deviceId,
      login: login,
      tokens: signedIn.tokens,
    );

    // Where this account belongs. Asked once, here, so that no screen ever
    // has to.
    final me = await auth.me(signedIn.tokens.access);
    for (final membership in me.households) {
      await _store(membership);
    }
  }

  /// Found a household. Whoever does it is its first admin.
  Future<Membership> createHousehold({
    required String name,
    required String displayName,
  }) async {
    final token = await _token();

    final membership = await auth.createHousehold(
      accessToken: token,
      name: name,
      displayName: displayName,
      tz: _timeZone,
    );

    await _store(
      Membership(
        householdId: membership.householdId,
        memberId: membership.memberId,
        role: membership.role,
        // The server does not echo it back, and the client already knows what
        // it asked for.
        name: membership.name ?? name,
      ),
    );

    return membership;
  }

  /// Join a household with the code somebody read out loud.
  Future<Membership> join({
    required String code,
    required String displayName,
  }) async {
    final token = await _token();

    final membership = await auth.redeemInvite(
      accessToken: token,
      code: code,
      displayName: displayName,
    );

    await _store(membership);
    return membership;
  }

  /// Renew the pair, keeping the device.
  ///
  /// A lost answer here is survivable by design: the server honours the old
  /// refresh token for a further minute, so a phone that retries after a
  /// dropped response is not locked out of its own household.
  Future<void> renew() async {
    final identity = await db.select(db.deviceIdentity).getSingleOrNull();
    final refresh = identity?.refreshToken;
    if (refresh == null || refresh.isEmpty) {
      throw const AuthException(AuthFailure.badCredentials, 'no refresh token');
    }

    final tokens = await auth.refresh(refresh);
    await _writeTokens(tokens);
  }

  /// Forget the credentials, keep the replica.
  ///
  /// Signing out is not being revoked. The rows stay: the same person signing
  /// back in on the same device picks up where they were, and a household's
  /// data is not thrown away because somebody lent their phone to a friend.
  Future<void> signOut() async {
    await db
        .update(db.deviceIdentity)
        .write(
          const DeviceIdentityCompanion(
            accessToken: Value(null),
            refreshToken: Value(null),
            accessUntil: Value(null),
          ),
        );
  }

  // --- the plumbing ---------------------------------------------------------

  Session _session(
    DeviceIdentityData identity,
    HouseholdMembership membership,
  ) {
    return Session(
      accountId: identity.accountId ?? '',
      deviceId: identity.deviceId ?? '',
      accessToken: identity.accessToken ?? '',
      householdId: membership.householdId,
      memberId: membership.memberId,
      role: membership.role,
      householdName: membership.name,
      login: identity.login,
    );
  }

  Future<String> _token() async {
    final identity = await db.select(db.deviceIdentity).getSingleOrNull();
    final token = identity?.accessToken;
    if (token == null || token.isEmpty) {
      throw const AuthException(AuthFailure.badCredentials, 'not signed in');
    }
    return token;
  }

  Future<void> _remember({
    required String accountId,
    required String deviceId,
    required String login,
    required TokenPair tokens,
  }) async {
    await db
        .into(db.deviceIdentity)
        .insertOnConflictUpdate(
          DeviceIdentityCompanion(
            id: const Value(1),
            accountId: Value(accountId),
            deviceId: Value(deviceId),
            login: Value(login),
            accessToken: Value(tokens.access),
            refreshToken: Value(tokens.refresh),
            accessUntil: Value(_expiryOf(tokens)),
          ),
        );
  }

  Future<void> _writeTokens(TokenPair tokens) async {
    await db
        .update(db.deviceIdentity)
        .write(
          DeviceIdentityCompanion(
            accessToken: Value(tokens.access),
            refreshToken: Value(tokens.refresh),
            accessUntil: Value(_expiryOf(tokens)),
          ),
        );
  }

  /// Lifetimes arrive as seconds, not as timestamps, so that a phone whose
  /// clock is wrong can still work out when to renew.
  int _expiryOf(TokenPair tokens) {
    return _wallClock()
        .add(Duration(seconds: tokens.accessExpiresIn))
        .millisecondsSinceEpoch;
  }

  /// Record a household, and make it the one on screen if nothing else is.
  Future<void> _store(Membership membership) async {
    await db.transaction(() async {
      final existing = await db.select(db.memberships).get();
      final isFirst = existing.isEmpty;

      await db
          .into(db.memberships)
          .insertOnConflictUpdate(
            MembershipsCompanion(
              householdId: Value(membership.householdId),
              memberId: Value(membership.memberId),
              role: Value(membership.role),
              name: Value(membership.name),
              // The first household lands on screen. A second one does not
              // take over: somebody joining their parent's household from
              // their own kitchen should not find their own chores gone.
              current: Value(isFirst ? 1 : 0),
            ),
          );
    });
  }

  /// Put a household on screen.
  Future<void> show(String householdId) async {
    await db.transaction(() async {
      await db
          .update(db.memberships)
          .write(const MembershipsCompanion(current: Value(0)));
      await (db.update(db.memberships)
            ..where((m) => m.householdId.equals(householdId)))
          .write(const MembershipsCompanion(current: Value(1)));
    });
  }
}
