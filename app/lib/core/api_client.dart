import 'dart:convert';

import 'package:choreo/core/config.dart';
import 'package:http/http.dart' as http;

/// What the server said when we last managed to ask it.
class ServerHealth {
  const ServerHealth._(this.isReachable, this.version);

  const ServerHealth.reachable(String version) : this._(true, version);

  const ServerHealth.unreachable() : this._(false, null);

  final bool isReachable;
  final String? version;
}

/// The thin client to the Choreo server.
///
/// Thin on purpose. The server is a sync relay, not the brain: nothing the
/// user sees is fetched from it on demand.
class ApiClient {
  ApiClient({http.Client? httpClient, String? baseUrl})
    : _http = httpClient ?? http.Client(),
      _baseUrl = baseUrl ?? AppConfig.serverBaseUrl;

  final http.Client _http;
  final String _baseUrl;

  /// Short on purpose: a phone on a flaky connection should fall back to its
  /// local data quickly rather than spin.
  static const Duration _timeout = Duration(seconds: 5);

  Future<ServerHealth> health() async {
    try {
      final response = await _http
          .get(Uri.parse('$_baseUrl/healthz'))
          .timeout(_timeout);

      if (response.statusCode != 200) {
        return const ServerHealth.unreachable();
      }

      final body = jsonDecode(response.body) as Map<String, dynamic>;
      return ServerHealth.reachable(body['version'] as String? ?? 'unknown');
    } on Exception {
      // Being offline is the normal case, not a failure: the whole app is
      // built around it. Nothing to report and nothing to log loudly.
      return const ServerHealth.unreachable();
    }
  }

  void close() => _http.close();
}
