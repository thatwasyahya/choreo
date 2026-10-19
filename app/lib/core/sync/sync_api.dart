import 'dart:convert';

import 'package:choreo/core/config.dart';
import 'package:http/http.dart' as http;

/// What the server decided about one gesture.
class OpOutcome {
  const OpOutcome({
    required this.opId,
    required this.outcome,
    this.code,
    this.rev = 0,
    this.noticeId,
  });

  factory OpOutcome.fromJson(Map<String, dynamic> json) {
    return OpOutcome(
      opId: json['op_id'] as String? ?? '',
      outcome: json['outcome'] as String? ?? '',
      code: json['code'] as String?,
      rev: (json['rev'] as num?)?.toInt() ?? 0,
      noticeId: json['notice_id'] as String?,
    );
  }

  final String opId;
  final String outcome;
  final String? code;
  final int rev;
  final String? noticeId;

  /// Whether the server is done with this gesture, whatever it decided.
  ///
  /// A refusal counts as settled unless it is retryable: `unsupported_op` means
  /// the server is older than this app, which a deploy fixes — dropping the
  /// gesture would poison a real person's outbox over an ordering mistake.
  bool get isSettled => !(outcome == 'refused' && code == 'unsupported_op');
}

/// One row state travelling down.
class RowChange {
  const RowChange({
    required this.entity,
    required this.rowId,
    required this.rev,
    required this.deleted,
    required this.state,
  });

  factory RowChange.fromJson(Map<String, dynamic> json) {
    return RowChange(
      entity: json['entity'] as String? ?? '',
      rowId: json['row_id'] as String? ?? '',
      rev: (json['rev'] as num?)?.toInt() ?? 0,
      deleted: json['deleted'] as bool? ?? false,
      state: (json['state'] as Map<String, dynamic>?) ?? const {},
    );
  }

  final String entity;
  final String rowId;
  final int rev;
  final bool deleted;
  final Map<String, dynamic> state;
}

/// One round trip.
class SyncResponse {
  const SyncResponse({
    required this.headSeq,
    required this.cursorSeq,
    required this.results,
    required this.changes,
    this.hasMore = false,
    this.purgeHorizonSeq = 0,
    this.snapshotRequired = false,
  });

  factory SyncResponse.fromJson(Map<String, dynamic> json) {
    return SyncResponse(
      headSeq: (json['head_seq'] as num?)?.toInt() ?? 0,
      cursorSeq: (json['cursor_seq'] as num?)?.toInt() ?? 0,
      results: ((json['results'] as List<dynamic>?) ?? const [])
          .map((r) => OpOutcome.fromJson(r as Map<String, dynamic>))
          .toList(),
      changes: ((json['changes'] as List<dynamic>?) ?? const [])
          .map((c) => RowChange.fromJson(c as Map<String, dynamic>))
          .toList(),
      hasMore: json['has_more'] as bool? ?? false,
      purgeHorizonSeq: (json['purge_horizon_seq'] as num?)?.toInt() ?? 0,
      snapshotRequired: json['snapshot_required'] as bool? ?? false,
    );
  }

  final int headSeq;
  final int cursorSeq;
  final List<OpOutcome> results;
  final List<RowChange> changes;
  final bool hasMore;
  final int purgeHorizonSeq;

  /// The cursor fell below what the server still keeps. The device has to
  /// rebuild from a snapshot — but the gestures it just pushed were applied
  /// and answered all the same.
  final bool snapshotRequired;
}

/// Why a round trip did not happen.
enum SyncFailure {
  /// No network, or the server did not answer. The normal case, not an error:
  /// the app is built around it.
  offline,

  /// The token is no longer good. The session has to be renewed.
  unauthenticated,

  /// This device was removed. The local replica must be wiped — which is what
  /// tells it apart from signing out.
  deviceRevoked,

  /// The server answered something this version cannot read.
  unreadable,
}

class SyncException implements Exception {
  const SyncException(this.failure, [this.detail]);

  final SyncFailure failure;
  final String? detail;

  @override
  String toString() => 'SyncException(${failure.name}, $detail)';
}

/// The thin client to the synchronisation endpoint.
class SyncApi {
  SyncApi({http.Client? httpClient, String? baseUrl})
    : _http = httpClient ?? http.Client(),
      _baseUrl = baseUrl ?? AppConfig.serverBaseUrl;

  final http.Client _http;
  final String _baseUrl;

  /// Long enough to cover a long poll, with room for a slow network on top.
  static const Duration _timeout = Duration(seconds: 40);

  Future<SyncResponse> sync({
    required String householdId,
    required String accessToken,
    required int cursorSeq,
    required List<Map<String, dynamic>> ops,
    int waitMs = 0,
    int limit = 500,
  }) async {
    final http.Response response;
    try {
      response = await _http
          .post(
            Uri.parse('$_baseUrl/v1/households/$householdId/sync'),
            headers: {
              'Content-Type': 'application/json',
              'Authorization': 'Bearer $accessToken',
            },
            body: jsonEncode({
              'protocol': 1,
              'app_version': AppConfig.version,
              'cursor_seq': cursorSeq,
              'limit': limit,
              'wait_ms': waitMs,
              'ops': ops,
            }),
          )
          .timeout(_timeout);
    } on Exception catch (error) {
      // Being unreachable is the normal case, not a failure worth shouting
      // about: the whole app is built around it.
      throw SyncException(SyncFailure.offline, error.toString());
    }

    switch (response.statusCode) {
      case 200:
        try {
          return SyncResponse.fromJson(
            jsonDecode(response.body) as Map<String, dynamic>,
          );
        } on Exception catch (error) {
          throw SyncException(SyncFailure.unreadable, error.toString());
        }

      case 204:
        // The long poll expired with nothing to say. Not an error, and not a
        // reason to change anything locally.
        return SyncResponse(
          headSeq: int.tryParse(response.headers['choreo-head-seq'] ?? '') ?? 0,
          cursorSeq: cursorSeq,
          results: const [],
          changes: const [],
        );

      case 401:
        final revoked = response.body.contains('device_revoked');
        throw SyncException(
          revoked ? SyncFailure.deviceRevoked : SyncFailure.unauthenticated,
        );

      default:
        throw SyncException(
          SyncFailure.unreadable,
          'status ${response.statusCode}',
        );
    }
  }

  void close() => _http.close();
}
