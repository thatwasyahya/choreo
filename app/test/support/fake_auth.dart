import 'dart:convert';

import 'package:choreo/core/auth_api.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

/// A server that says yes.
///
/// Shared by the session tests and the onboarding tests so that both talk to
/// the same answers: a fake that drifts between test files is a fake that
/// proves two different things.
AuthApi agreeableAuth({
  List<http.Request>? seen,
  Map<String, dynamic>? me,
  String recoveryCode = 'K7MN-4PQR-8STV-2WXY-3Z5A-6BCD',
}) {
  return AuthApi(
    platform: 'linux',
    baseUrl: 'http://example.invalid',
    httpClient: MockClient((request) async {
      seen?.add(request);

      switch (request.url.path) {
        case '/v1/accounts':
          return _json({
            'account_id': 'account-1',
            'device_id': 'device-1',
            'access': 'access-1',
            'access_expires_in': 900,
            'refresh': 'refresh-1',
            'refresh_expires_in': 15552000,
            'recovery_code': recoveryCode,
          }, 201);

        case '/v1/sessions':
          return _json({
            'account_id': 'account-1',
            'device_id': 'device-1',
            'access': 'access-2',
            'access_expires_in': 900,
            'refresh': 'refresh-2',
            'refresh_expires_in': 15552000,
          }, 200);

        case '/v1/sessions/refresh':
          return _json({
            'access': 'access-3',
            'access_expires_in': 900,
            'refresh': 'refresh-3',
            'refresh_expires_in': 15552000,
          }, 200);

        case '/v1/me':
          return _json(
            me ??
                {
                  'account_id': 'account-1',
                  'login': 'yahya',
                  'device_id': 'device-1',
                  'households': [
                    {
                      'household_id': 'home',
                      'member_id': 'member-me',
                      'name': 'Maison',
                      'role': 'admin',
                    },
                  ],
                },
            200,
          );

        case '/v1/households':
          return _json({
            'household_id': 'home',
            'member_id': 'member-me',
            'role': 'admin',
          }, 201);

        case '/v1/invites/redeem':
          return _json({
            'household_id': 'other-home',
            'member_id': 'member-guest',
            'role': 'adult',
          }, 200);

        case '/v1/households/home/invites':
          return _json({
            'code': 'K7MN4PQR8S',
            'qr': 'choreo://join?s=http%3A%2F%2Fexample.invalid&c=K7MN4PQR8S',
            'expires_at': '2026-10-22T14:30:00Z',
          }, 201);

        default:
          return http.Response('{"error":"not_found"}', 404);
      }
    }),
  );
}

/// A server that refuses with one code.
AuthApi refusingAuth(int status, String code) {
  return AuthApi(
    platform: 'linux',
    baseUrl: 'http://example.invalid',
    httpClient: MockClient(
      (_) async => http.Response('{"error":"$code"}', status),
    ),
  );
}

/// A server that is not there.
AuthApi unreachableAuth() {
  return AuthApi(
    platform: 'linux',
    baseUrl: 'http://example.invalid',
    httpClient: MockClient((_) async => throw http.ClientException('offline')),
  );
}

Map<String, dynamic> bodyOf(http.Request request) {
  return jsonDecode(request.body) as Map<String, dynamic>;
}

http.Response _json(Map<String, dynamic> body, int status) {
  return http.Response(
    jsonEncode(body),
    status,
    headers: {'content-type': 'application/json'},
  );
}
