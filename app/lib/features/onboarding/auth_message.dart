import 'package:choreo/core/auth_api.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';

/// One place where a failure becomes a sentence.
///
/// The server sends codes, never prose — a server that returns sentences
/// decides the wording for every client forever, and gets it wrong in both
/// languages. This is the lookup, and it lives apart from the screens so that
/// no screen is tempted to invent its own wording for the same code.
String messageFor(AppLocalizations l10n, AuthFailure failure) {
  switch (failure) {
    case AuthFailure.offline:
      return l10n.errOffline;
    case AuthFailure.loginTaken:
      return l10n.errLoginTaken;
    case AuthFailure.weakPassword:
      return l10n.errWeakPassword;
    case AuthFailure.invalidLogin:
      return l10n.errInvalidLogin;
    case AuthFailure.badCredentials:
      return l10n.errBadCredentials;
    case AuthFailure.inviteUnknown:
      return l10n.errInviteUnknown;
    case AuthFailure.alreadyMember:
      return l10n.errAlreadyMember;
    case AuthFailure.rateLimited:
      return l10n.errRateLimited;
    case AuthFailure.server:
      return l10n.errServer;
  }
}
