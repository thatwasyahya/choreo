import 'package:choreo/design/tokens.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// What somebody asked for in the create sheet.
///
/// Either a task, now, or the same words carried into the screen where a chore
/// is made to repeat. The sheet itself never asks "does this repeat?" — that
/// would be a second decision on the ten-second path.
class NewTask {
  const NewTask(this.title, {this.repeating = false});

  final String title;

  /// They tapped "Repeat…": the title travels, the sheet closes, and the
  /// schedule is chosen on a screen with room for it.
  final bool repeating;
}

/// Ask for a title, and for nothing else.
///
/// Product principle 1 is a criterion here, not an intention: ten seconds, one
/// hand, in aeroplane mode. That budget is what rules out everything a form
/// would normally carry — a due date, an assignee, a category, a priority. One
/// field, already focused, and the keyboard's own action key creates the task.
/// Due dates and the rest are reachable afterwards, on a task that exists.
Future<NewTask?> showNewTaskSheet(BuildContext context) {
  return showModalBottomSheet<NewTask>(
    context: context,
    // So the sheet rides above the keyboard instead of hiding under it.
    isScrollControlled: true,
    builder: (context) => const _NewTaskSheet(),
  );
}

class _NewTaskSheet extends StatefulWidget {
  const _NewTaskSheet();

  @override
  State<_NewTaskSheet> createState() => _NewTaskSheetState();
}

class _NewTaskSheetState extends State<_NewTaskSheet> {
  final TextEditingController _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _submit() {
    final title = _controller.text.trim();
    // An empty title closes the sheet and creates nothing. Deliberately no
    // error message: somebody who opened it by accident has nothing to fix.
    Navigator.of(context).pop(title.isEmpty ? null : NewTask(title));
  }

  /// Hand the words to the screen that asks how often.
  ///
  /// Allowed to be empty: "set up a repeating chore" is a perfectly good
  /// intention to arrive with, and the next screen asks for the title anyway.
  void _repeat() {
    Navigator.of(context).pop(
      NewTask(_controller.text.trim(), repeating: true),
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    return Padding(
      padding: EdgeInsets.only(
        left: Space.gutter,
        right: Space.gutter,
        top: Space.snug,
        bottom: MediaQuery.viewInsetsOf(context).bottom + Space.wide,
      ),
      child: SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _controller,
                    // The whole criterion rests on this line: one tap on the
                    // button and the keyboard is already up, waiting.
                    autofocus: true,
                    textInputAction: TextInputAction.done,
                    textCapitalization: TextCapitalization.sentences,
                    decoration: InputDecoration(hintText: l10n.newTaskHint),
                    onSubmitted: (_) => _submit(),
                  ),
                ),
                const SizedBox(width: Space.step),
                // A second way out for the thumb, for anyone whose keyboard
                // shows no action key. The same path, so there is one way to
                // create a task and not two.
                //
                // Square-ish rather than round, because the floating button
                // that opened this sheet is square-ish: one accent, one shape.
                // A circle here made the same action look like a different one.
                SizedBox(
                  height: Touch.min,
                  width: Touch.min,
                  child: IconButton.filled(
                    onPressed: _submit,
                    tooltip: l10n.addTask,
                    style: IconButton.styleFrom(
                      shape: const RoundedRectangleBorder(
                        borderRadius: Radii.card,
                      ),
                    ),
                    icon: const Icon(Icons.arrow_upward, size: 20),
                  ),
                ),
              ],
            ),
            // The deliberate path, and deliberately quiet: a text button under
            // the field rather than a second control beside it. Somebody
            // adding "milk" never has to read it; somebody setting up bin day
            // finds it exactly where they would look.
            TextButton(
              onPressed: _repeat,
              child: Text(l10n.repeatAction),
            ),
          ],
        ),
      ),
    );
  }
}
