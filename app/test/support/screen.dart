import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// `testWidgets`, plus the cleanup a Drift screen needs.
///
/// Cancelling a Drift subscription schedules a zero-duration timer. A test
/// ending in the same frame leaves that timer pending, and flutter_test then
/// fails the test over it — a complaint about the plumbing that says nothing
/// about what was being checked, with a stack that accuses Flutter.
/// Unmounting the tree and pumping once lets it run.
void screenTest(String description, Future<void> Function(WidgetTester) body) {
  testWidgets(description, (tester) async {
    await body(tester);

    await tester.pumpWidget(const SizedBox.shrink());

    // A millisecond, not a zero-length pump: the timer Drift schedules on
    // cancellation is due at the current instant, and the clock has to move for
    // it to be due in the past.
    await tester.pump(const Duration(milliseconds: 1));
  });
}
