import 'package:choreo/design/tokens.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:flutter/material.dart';

/// Ask for a title, and for nothing else.
///
/// Product principle 1 is a criterion here, not an intention: ten seconds, one
/// hand, in aeroplane mode. That budget is what rules out everything a form
/// would normally carry — a due date, an assignee, a category, a priority. One
/// field, already focused, and the keyboard's own action key creates the task.
/// Due dates and the rest are reachable afterwards, on a task that exists.
Future<String?> showNewTaskSheet(BuildContext context) {
  return showModalBottomSheet<String>(
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
    Navigator.of(context).pop(title.isEmpty ? null : title);
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
        child: Row(
          children: [
            Expanded(
              child: TextField(
                controller: _controller,
                // The whole criterion rests on this line: one tap on the
                // button and the keyboard is already up, waiting.
                autofocus: true,
                textInputAction: TextInputAction.done,
                textCapitalization: TextCapitalization.sentences,
                decoration: InputDecoration(
                  hintText: l10n.newTaskHint,
                  border: const OutlineInputBorder(),
                ),
                onSubmitted: (_) => _submit(),
              ),
            ),
            const SizedBox(width: Space.step),
            // A second way out for the thumb, for anyone whose keyboard shows
            // no action key. The same path, so there is one way to create a
            // task and not two.
            //
            // Square-ish rather than round, because the floating button that
            // opened this sheet is square-ish: one accent, one shape. A circle
            // here made the same action look like a different one.
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
      ),
    );
  }
}
