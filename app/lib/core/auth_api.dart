import 'dart:convert';
import 'dart:io' show Platform;

import 'package:choreo/core/config.dart';
import 'package:http/http.dart' as http;

/// A pair of tokens and how long they last.
///
/// Lifetimes arrive as seconds rather than timestamps, so a phone whose clock
/// is wrong can still work out when to renew.
class TokenPair {
  const TokenPair({
    required this.access,
    required this.refresh,
    required this.accessExpiresIn,
    required this.refreshExpiresIn,
  });

  factory TokenPair.fromJson(Map<String, dynamic> json) {
    return TokenPair(
      access: json['access'] as String? ?? '',
      refresh: json['refresh'] as String? ?? '',
      accessExpiresIn: (json['access_expires_in'] as num?)?.toInt() ?? 0,
      refreshExpiresIn: (json['refresh_expires_in'] as num?)?.toInt() ?? 0,
    );
  }

  final String access;
  final String refresh;
  final int accessExpiresIn;
  final int refreshExpiresIn;
}

/// A brand new account.
class NewAccount {
  const NewAccount({
    required this.accountId,
    required this.deviceId,
    required this.tokens,
    required this.recoveryCode,
  });

  factory NewAccount.fromJson(Map<String, dynamic> json) {
    return NewAccount(
      accountId: json['account_id'] as String? ?? '',
      deviceId: json['device_id'] as String? ?? '',
      tokens: TokenPair.fromJson(json),
      recoveryCode: json['recovery_code'] as String? ?? '',
    );
  }

  final String accountId;
  final String deviceId;
  final TokenPair tokens;

  /// Shown once and never again. There is no e-mail address in this product,
  /// so this code is the only way back in — the screen that receives it has to
  /// make the person write it down before moving on.
  final String recoveryCode;
}

/// An account signing back in.
class SignedIn {
  const SignedIn({
    required this.accountId,
    required this.deviceId,
    required this.tokens,
  });

  factory SignedIn.fromJson(Map<String, dynamic> json) {
    return SignedIn(
      accountId: json['account_id'] as String? ?? '',
      deviceId: json['device_id'] as String? ?? '',
      tokens: TokenPair.fromJson(json),
    );
  }

  final String accountId;
  final String deviceId;
  final TokenPair tokens;
}

/// This account's place in one household.
class Membership {
  const Membership({
    required this.householdId,
    required this.memberId,
    required this.role,
    this.name,
  });

  factory Membership.fromJson(Map<String, dynamic> json) {
    return Membership(
      householdId: json['household_id'] as String? ?? '',
      memberId: json['member_id'] as String? ?? '',
      role: json['role'] as String? ?? 'adult',
      name: json['name'] as String?,
    );
  }

  final String householdId;
  final String memberId;
  final String role;

  /// The household's name, when the server sent one. Absent from the answer to
  /// "I have just created it", where the client already knows what it asked
  /// for.
  final String? name;
}

/// Who the server thinks we are, and where we belong.
class Me {
  const Me({
    required this.accountId,
    required this.login,
    required this.deviceId,
    required this.households,
  });

  factory Me.fromJson(Map<String, dynamic> json) {
    final households = (json['households'] as List<dynamic>? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(Membership.fromJson)
        .toList();

    return Me(
      accountId: json['account_id'] as String? ?? '',
      login: json['login'] as String? ?? '',
      deviceId: json['device_id'] as String? ?? '',
      households: households,
    );
  }

  final String accountId;
  final String login;
  final String deviceId;
  final List<Membership> households;
}

/// A code to pass on to somebody.
class Invite {
  const Invite({
    required this.code,
    required this.join,
    required this.expiresAt,
  });

  factory Invite.fromJson(Map<String, dynamic> json) {
    return Invite(
      code: json['code'] as String? ?? '',
      join: json['qr'] as String? ?? '',
      expiresAt: DateTime.tryParse(json['expires_at'] as String? ?? ''),
    );
  }

  /// Ten characters of Crockford base32 — no I, L, O or U — so it survives
  /// being read out loud across a kitchen.
  final String code;

  /// What goes behind the QR code. It carries the SERVER ADDRESS as well as
  /// the code: without it, a household running its own instance could not
  /// invite anybody (product principle 8).
  final String join;

  final DateTime? expiresAt;
}

/// Why an attempt did not work.
///
/// Codes, never sentences. The server returns localisation keys — a server that
/// returns prose decides the wording for every client forever, and gets it
/// wrong in both languages.
enum AuthFailure {
  /// No network, or the server did not answer.
  offline,

  /// That login is taken. Said plainly, because a login here is a name chosen
  /// in a household and not an e-mail address worth protecting.
  loginTaken,

  weakPassword,
  invalidLogin,

  /// Login or password wrong, and deliberately not which.
  badCredentials,

  /// The invite code is unknown, used up or expired, and deliberately not
  /// which: telling them apart would turn the endpoint into a code oracle.
  inviteUnknown,

  alreadyMember,

  /// The server does not know this device's time zone. Nothing the person can
  /// act on, but worth naming: it is the kind of thing a brand new zone or an
  /// out-of-date server causes, and "something went wrong" would hide it.
  invalidTimeZone,

  /// Too many attempts. Not a failure of the person's, and the screen says so.
  rateLimited,

  /// The server answered something this version cannot read, or broke.
  server,
}

class AuthException implements Exception {
  const AuthException(this.failure, [this.detail]);

  final AuthFailure failure;
  final String? detail;

  @override
  String toString() => 'AuthException(${failure.name}, $detail)';
}

/// The client to everything that happens before synchronisation can.
///
/// Four calls, and they are the only ones in the app that go to the network
/// without a local answer to fall back on: there is no offline way to prove who
/// you are to a server that has never met you.
class AuthApi {
  AuthApi({http.Client? httpClient, String? baseUrl, String? platform})
    : _http = httpClient ?? http.Client(),
      _baseUrl = baseUrl ?? AppConfig.serverBaseUrl,
      _platform = platform ?? _thisPlatform();

  final http.Client _http;
  final String _baseUrl;
  final String _platform;

  /// Short, like everywhere else: a phone on a flaky connection should hear
  /// "not now" quickly rather than spin.
  static const Duration _timeout = Duration(seconds: 15);

  static String _thisPlatform() {
    if (Platform.isAndroid) {
      return 'android';
    }
    if (Platform.isIOS) {
      return 'ios';
    }
    return 'linux';
  }

  Future<NewAccount> createAccount({
    required String login,
    required String password,
  }) async {
    final body = await _post('/v1/accounts', {
      'login': login,
      'password': password,
      'protocol': 1,
      'app_version': AppConfig.version,
      'platform': _platform,
    });

    return NewAccount.fromJson(body);
  }

  /// Sign in, presenting the device id this installation already has.
  ///
  /// Presenting it is what lets a phone sign back in without losing its
  /// cursor — and so without having to rebuild its replica from a snapshot.
  Future<SignedIn> signIn({
    required String login,
    required String password,
    String? deviceId,
  }) async {
    final body = await _post('/v1/sessions', {
      'login': login,
      'password': password,
      'protocol': 1,
      'app_version': AppConfig.version,
      'platform': _platform,
      // Absent rather than null when this installation has none yet: the
      // server mints one, and sending null would ask it to parse nothing.
      'device_id': ?deviceId,
    });

    return SignedIn.fromJson(body);
  }

  Future<TokenPair> refresh(String refreshToken) async {
    final body = await _post('/v1/sessions/refresh', {'refresh': refreshToken});
    return TokenPair.fromJson(body);
  }

  /// Where this account belongs.
  ///
  /// Asked once after signing in, to fill in what the phone must know before
  /// it can render anything — and never again on a screen's critical path.
  Future<Me> me(String accessToken) async {
    final body = await _get('/v1/me', accessToken);
    return Me.fromJson(body);
  }

  Future<Membership> createHousehold({
    required String accessToken,
    required String name,
    required String displayName,
    required String tz,
  }) async {
    final body = await _post('/v1/households', {
      'name': name,
      'display_name': displayName,
      'tz': tz,
    }, accessToken: accessToken);

    return Membership.fromJson(body);
  }

  /// Mint a code for somebody to join with. Admins only, and never for an
  /// admin: a leaked code must not be able to hand over the household.
  Future<Invite> createInvite({
    required String accessToken,
    required String householdId,
    String role = 'adult',
  }) async {
    final body = await _post('/v1/households/$householdId/invites', {
      'role': role,
    }, accessToken: accessToken);

    return Invite.fromJson(body);
  }

  /// Join a household with the code somebody read out loud.
  Future<Membership> redeemInvite({
    required String accessToken,
    required String code,
    required String displayName,
  }) async {
    final body = await _post('/v1/invites/redeem', {
      'code': code,
      'display_name': displayName,
    }, accessToken: accessToken);

    return Membership.fromJson(body);
  }

  Future<Map<String, dynamic>> _post(
    String path,
    Map<String, dynamic> payload, {
    String? accessToken,
  }) {
    return _send(
      () => _http.post(
        Uri.parse('$_baseUrl$path'),
        headers: {
          'Content-Type': 'application/json',
          if (accessToken != null) 'Authorization': 'Bearer $accessToken',
        },
        body: jsonEncode(payload),
      ),
    );
  }

  Future<Map<String, dynamic>> _get(String path, String accessToken) {
    return _send(
      () => _http.get(
        Uri.parse('$_baseUrl$path'),
        headers: {'Authorization': 'Bearer $accessToken'},
      ),
    );
  }

  Future<Map<String, dynamic>> _send(
    Future<http.Response> Function() call,
  ) async {
    final http.Response response;
    try {
      response = await call().timeout(_timeout);
    } on Exception catch (error) {
      throw AuthException(AuthFailure.offline, error.toString());
    }

    if (response.statusCode == 200 || response.statusCode == 201) {
      try {
        return jsonDecode(response.body) as Map<String, dynamic>;
      } on Exception catch (error) {
        throw AuthException(AuthFailure.server, error.toString());
      }
    }

    throw AuthException(_failureOf(response), 'status ${response.statusCode}');
  }

  /// The server's code, mapped onto something a screen can say.
  ///
  /// An unknown code becomes [AuthFailure.server] rather than a guess: a newer
  /// server may have a code this version has never heard of, and inventing a
  /// sentence for it would be worse than a plain "something went wrong".
  static AuthFailure _failureOf(http.Response response) {
    final String code;
    try {
      code =
          (jsonDecode(response.body) as Map<String, dynamic>)['error']
              as String? ??
          '';
    } on Exception {
      return AuthFailure.server;
    }

    switch (code) {
      case 'login_taken':
        return AuthFailure.loginTaken;
      case 'weak_password':
        return AuthFailure.weakPassword;
      case 'invalid_login':
        return AuthFailure.invalidLogin;
      case 'invalid_credentials':
      case 'unauthenticated':
        return AuthFailure.badCredentials;
      case 'invite_unknown':
        return AuthFailure.inviteUnknown;
      case 'already_member':
        return AuthFailure.alreadyMember;
      case 'invalid_tz':
        return AuthFailure.invalidTimeZone;
      case 'rate_limited':
        return AuthFailure.rateLimited;
      default:
        return AuthFailure.server;
    }
  }

  void close() => _http.close();
}
