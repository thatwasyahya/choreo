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
}
