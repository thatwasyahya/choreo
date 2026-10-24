import 'package:choreo/core/sync/sync_driver.dart';
import 'package:flutter/material.dart';

/// Tells the driver when the app is on screen.
///
/// A long poll belongs in the foreground and nowhere else: Android kills a
/// parked socket in the background, and `D-010` rules out a push service. So
/// the loop needs to know about the lifecycle, and the lifecycle is a widget's
/// concern — which is the whole reason this wrapper exists rather than the
/// driver reaching for `WidgetsBinding` itself. The driver stays testable
/// without a widget tree.
class SyncHost extends StatefulWidget {
  const SyncHost({required this.driver, required this.child, super.key});

  final SyncDriver driver;
  final Widget child;

  @override
  State<SyncHost> createState() => _SyncHostState();
}

class _SyncHostState extends State<SyncHost> with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);

    // Follow the session, and catch up straight away: an app opened after two
    // days in a drawer should not wait for a timer to show what the household
    // did meanwhile.
    widget.driver
      ..follow()
      ..resumed();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    switch (state) {
      case AppLifecycleState.resumed:
        widget.driver.resumed();

      case AppLifecycleState.inactive:
      case AppLifecycleState.hidden:
      case AppLifecycleState.paused:
      case AppLifecycleState.detached:
        // The parked call is left to expire rather than cancelled: the server
        // answers 204 and nothing is lost.
        widget.driver.paused();
    }
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
