/// Where the app talks to.
///
/// The server address is a build-time parameter, never a hard-coded constant.
/// A household that wants to run its own server has to be able to point the
/// app at it — that is product principle 8, and it only stays true if nothing
/// in the code assumes a single address.
///
/// Override it at build time:
///   flutter build apk --dart-define=CHOREO_SERVER_URL=https://choreo.example
abstract final class AppConfig {
  static const String serverBaseUrl = String.fromEnvironment(
    'CHOREO_SERVER_URL',
    defaultValue: 'http://localhost:8080',
  );

  /// Sent on every synchronisation call, and stored by the server next to each
  /// operation. It is what lets somebody look at a strange row at two in the
  /// morning and know which version of the app wrote it.
  static const String version = String.fromEnvironment(
    'CHOREO_APP_VERSION',
    defaultValue: 'dev',
  );

  /// The wire vocabulary this app speaks.
  ///
  /// Version 2 added recurring chores. The number is what lets a server tell
  /// "an app too old to know about series" from "an app with a bug", and it is
  /// why adding an entity to the vocabulary is an increment rather than a
  /// tweak. A server that only speaks 1 answers 426 and says what it has.
  static const int protocol = 2;
}
