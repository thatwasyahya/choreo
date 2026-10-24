import 'dart:async';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/session.dart';
import 'package:choreo/core/sync/sync_api.dart';
import 'package:choreo/core/sync/sync_engine.dart';
import 'package:drift/drift.dart';

/// What the person is told about synchronisation, and all they are told.
///
/// A count and an age, never a spinner. "Three gestures to send, the oldest
/// for ten days" is honest and actionable; a turning wheel says nothing and
/// hides a queue that has been stuck since Tuesday — which the specification
/// names as the failure that makes people leave.
class SyncStatus {
  const SyncStatus({
    this.pending = 0,
    this.oldestPendingMs,
    this.quarantined = 0,
    this.lastOutcome,
    this.rebuilding = false,
  });

  final int pending;

  /// How long the oldest queued gesture has been waiting, in monotonic
  /// milliseconds. Null when nothing is queued.
  final int? oldestPendingMs;

  final int quarantined;

  /// What the last round trip did: `applied`, `offline`, `unauthenticated`.
  /// A localisation key, never a sentence.
  final String? lastOutcome;

  final bool rebuilding;

  bool get isOffline => lastOutcome == 'offline';
}

/// The loop that drives the engine.
///
/// The engine knew how to make one round trip and nothing called it: every
/// gesture landed in the outbox and stayed there, so two phones never
/// converged in the real app. This is the piece that was missing.
///
/// Three rules shape it:
///
/// **One round trip at a time.** The protocol allows a single batch in flight,
/// and overlapping calls would push the same operations twice and race on the
/// cursor. A guard, not a queue: a second request while one is running is
/// dropped, because the running one will pick up whatever was added.
///
/// **Backoff in monotonic ticks, never wall clock.** A phone's clock moves
/// backwards when the network corrects it, and a backoff computed from one goes
/// haywire — forty years long, or zero.
///
/// **A long poll only in the foreground.** Android kills a parked socket in the
/// background and `D-010` rules out a push service, so the background path is a
/// periodic task rather than a held connection. That task is its own lot; what
/// this driver promises is that an app on screen is up to date within a second
/// of somebody else's write.
class SyncDriver {
  SyncDriver({
    required this.db,
    required this.engine,
    required this.sessions,
    int Function()? elapsed,
    this.foregroundWaitMs = 20000,
  }) : _elapsed = elapsed ?? _monotonic;

  final AppDatabase db;
  final SyncEngine engine;
  final SessionRepository sessions;

  /// How long the server may park a call with nothing to say. Twenty seconds
  /// keeps a shopping list feeling live — one round trip, 200 to 400 ms — for
  /// the price of a goroutine on the server and no connection in its pool.
  final int foregroundWaitMs;

  final int Function() _elapsed;

  static int _monotonic() => DateTime.now().millisecondsSinceEpoch;

  /// Backoff bounds, from the protocol: a second, then doubling, capped at five
  /// minutes. The cap matters more than the curve — a phone that has been in a
  /// cellar all afternoon must not then wait an hour.
  static const int _minBackoffMs = 1000;
  static const int _maxBackoffMs = 5 * 60 * 1000;

  final StreamController<SyncStatus> _status =
      StreamController<SyncStatus>.broadcast();

  String? _householdId;
  bool _inFlight = false;
  bool _foreground = true;
  bool _stopped = false;
  int _backoffMs = _minBackoffMs;
  Timer? _next;
  StreamSubscription<SessionState>? _watch;
  StreamSubscription<List<OutboxData>>? _outbox;

  /// What to show about synchronisation, as it changes.
  Stream<SyncStatus> watch() => _status.stream;

  /// Follow the session: drive while a household is on screen, stop otherwise.
  ///
  /// Started from the session rather than from a screen, because the loop has
  /// to survive navigation — a long poll that dies when somebody opens the
  /// member list is not a long poll.
  void follow() {
    // A gesture asks for a round trip by existing. Watching the queue rather
    // than having each screen call in keeps synchronisation out of the widgets
    // entirely: the task list does not know this class exists.
    //
    // The bound worth naming: while a long poll is parked, a gesture made in
    // that window waits for the poll to return — up to [foregroundWaitMs]
    // before it leaves the phone. Invisible to whoever made it, since their
    // screen already shows it, but it delays the other phone. Interrupting a
    // parked call needs a cancellable client, and that is its own change.
    _outbox ??= db.select(db.outbox).watch().listen((queued) {
      if (queued.isNotEmpty) {
        unawaited(syncNow());
      }
    });

    _watch ??= sessions.watchState().listen((state) {
      final household = state.session?.householdId;
      if (household == _householdId) {
        return;
      }
      _householdId = household;
      if (household == null) {
        _cancelNext();
      } else {
        _backoffMs = _minBackoffMs;
        unawaited(_tick());
      }
    });
  }

  /// The app came back on screen: catch up now, then park a long poll.
  void resumed() {
    _foreground = true;
    _backoffMs = _minBackoffMs;
    unawaited(_tick());
  }

  /// The app left the screen. The parked call is left to expire on its own
  /// rather than cancelled: the server answers 204 and nothing is lost.
  void paused() {
    _foreground = false;
    _cancelNext();
  }

  /// Ask for a round trip now — after a gesture, or from a pull to refresh.
  Future<void> syncNow() => _tick(wait: false);

  /// The household on screen, read from the local database.
  ///
  /// One query, and it answers the only question the loop has: whose rows am I
  /// synchronising. Null means nobody is signed in, or nobody has a household
  /// yet — both of which mean there is nothing to do.
  Future<String?> _currentHousehold() async {
    final state = await sessions.watchState().first;
    return _householdId = state.session?.householdId;
  }

  Future<void> dispose() async {
    _stopped = true;
    _cancelNext();
    await _watch?.cancel();
    await _outbox?.cancel();
    await _status.close();
  }

  // --- the loop -------------------------------------------------------------

  Future<void> _tick({bool wait = true}) async {
    if (_stopped || _inFlight) {
      return;
    }

    // Resolved here rather than only in `follow`, so that a round trip asked
    // for directly — after a gesture, or from a pull to refresh — works on a
    // driver nobody has subscribed yet. Caching it in `follow` alone made
    // every such call a silent no-op.
    final household = _householdId ?? await _currentHousehold();
    if (_stopped || household == null || _inFlight) {
      // A call already running will pick up anything queued since, so a second
      // one would only duplicate the push.
      return;
    }

    _inFlight = true;
    try {
      final waitMs = (wait && _foreground) ? foregroundWaitMs : 0;
      final report = await engine.syncOnce(household, waitMs: waitMs);

      if (report.snapshotRequired) {
        await _rebuild(household);
      }

      _backoffMs = _minBackoffMs;
      await _publish(outcome: 'applied');

      // More pages, or gestures queued while this call was in flight: go
      // again at once rather than wait for the next poll.
      final again = report.hasMore || await _hasQueued(household);

      if (_foreground) {
        // Reconnect immediately: the WAITING is the server's job, parked for
        // up to twenty seconds, which is what makes somebody else's write
        // land here in one round trip.
        _schedule(0);
      } else if (again) {
        // Off screen there is no parked call, so a zero delay would be a
        // tight loop against the server. Drain what is queued, slowly, and
        // then stop: the periodic background task is its own lot.
        _schedule(_minBackoffMs);
      }
    } on SyncException catch (error) {
      await _onFailure(household, error);
    } finally {
      _inFlight = false;
    }
  }

  Future<void> _onFailure(String household, SyncException error) async {
    switch (error.failure) {
      case SyncFailure.offline:
        // The ordinary case, not an error: the app is built around it. Back
        // off, say so plainly, and keep the queue.
        await _publish(outcome: 'offline');
        _scheduleBackoff();

      case SyncFailure.unauthenticated:
        await _renewOrStop();

      case SyncFailure.deviceRevoked:
        // Not the same as signing out: a revoked device must not keep a
        // readable replica of a household it was removed from.
        await _publish(outcome: 'device_revoked');
        await _wipe(household);

      case SyncFailure.unreadable:
        // The server said something this version cannot parse. Retrying will
        // not help until one of the two is deployed again, so back off hard
        // rather than hammer it.
        await _publish(outcome: 'unreadable');
        _scheduleBackoff();
    }
  }

  /// Renew once, then carry on. A lost answer here is survivable by design:
  /// the server honours the old refresh token for a further minute, so a
  /// retry after a dropped response is not a lock-out.
  Future<void> _renewOrStop() async {
    try {
      await sessions.renew();
      _backoffMs = _minBackoffMs;
      _schedule(0);
    } on Exception {
      // The refresh token is gone or refused. Forgetting the tokens sends the
      // shell back to the way in, with the replica kept.
      await _publish(outcome: 'unauthenticated');
      await sessions.signOut();
    }
  }

  Future<void> _rebuild(String household) async {
    await _publish(outcome: 'rebuilding', rebuilding: true);
    await engine.rebuild(household);
  }

  /// Forget everything about this household, locally.
  ///
  /// Deliberately not a sign-out: the device was removed from the household,
  /// so the rows have to go. The account's own credentials are cleared too,
  /// because the next call would only be refused again.
  Future<void> _wipe(String household) async {
    await db.transaction(() async {
      await (db.delete(db.tasks)
            ..where((t) => t.householdId.equals(household)))
          .go();
      await (db.delete(db.notices)
            ..where((n) => n.householdId.equals(household)))
          .go();
      await (db.delete(db.members)
            ..where((m) => m.householdId.equals(household)))
          .go();
      await (db.delete(db.outbox)
            ..where((o) => o.householdId.equals(household)))
          .go();
      await (db.delete(db.quarantine)
            ..where((q) => q.householdId.equals(household)))
          .go();
      await (db.delete(db.syncCursors)
            ..where((c) => c.householdId.equals(household)))
          .go();
      await (db.delete(db.memberships)
            ..where((m) => m.householdId.equals(household)))
          .go();
    });

    await sessions.signOut();
  }

  // --- scheduling -----------------------------------------------------------

  void _schedule(int ms) {
    if (_stopped || _householdId == null) {
      return;
    }
    _cancelNext();
    _next = Timer(Duration(milliseconds: ms), () => unawaited(_tick()));
  }

  void _scheduleBackoff() {
    _schedule(_backoffMs);
    _backoffMs = (_backoffMs * 2).clamp(_minBackoffMs, _maxBackoffMs);
  }

  void _cancelNext() {
    _next?.cancel();
    _next = null;
  }

  // --- what the screen is told ----------------------------------------------

  Future<bool> _hasQueued(String household) async {
    final waiting = await _queued(household);
    return waiting.isNotEmpty;
  }

  Future<List<OutboxData>> _queued(String household) {
    return (db.select(db.outbox)
          ..where((o) => o.householdId.equals(household))
          ..orderBy([(o) => OrderingTerm.asc(o.lseq)]))
        .get();
  }

  Future<void> _publish({
    required String outcome,
    bool rebuilding = false,
  }) async {
    final household = _householdId;
    if (_status.isClosed || household == null) {
      return;
    }

    final waiting = await _queued(household);
    final oldest = waiting.isEmpty
        ? null
        : _elapsed() - waiting.first.enqueuedElapsed;

    _status.add(
      SyncStatus(
        pending: waiting.length,
        oldestPendingMs: oldest,
        quarantined: await db.quarantinedCount(),
        lastOutcome: outcome,
        rebuilding: rebuilding,
      ),
    );
  }
}
