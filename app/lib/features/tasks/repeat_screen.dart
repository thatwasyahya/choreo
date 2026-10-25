import 'dart:async';
import 'dart:convert';

import 'package:choreo/core/database.dart';
import 'package:choreo/core/series_repository.dart';
import 'package:choreo/design/tokens.dart';
import 'package:choreo/l10n/generated/app_localizations.dart';
import 'package:drift/drift.dart' show OrderingTerm;
import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

/// How often, in the four shapes a household actually uses.
enum _Cadence { daily, weekly, monthly, afterDone }

/// Setting up a chore that comes back.
///
/// Deliberately NOT part of the ten-second path. Creating a task stays one
/// field and one tap, because that is the gesture people make twenty times a
/// week; "bin day is every Tuesday" is decided once, carefully, and deserves a
/// screen where the choice is visible rather than hidden behind a long press.
///
/// Everything here is a local write. The rule goes up as an ordinary queued
/// operation, and the first occurrence comes back from the server — predicting
/// it here is the one way a client could break the one-open-occurrence
/// invariant.
class RepeatScreen extends StatefulWidget {
  const RepeatScreen({
    required this.db,
    required this.series,
    this.title = '',
    super.key,
  });

  final AppDatabase db;
  final SeriesRepository series;

  /// Carried over from the create sheet, so nobody types it twice.
  final String title;

  @override
  State<RepeatScreen> createState() => _RepeatScreenState();
}

class _RepeatScreenState extends State<RepeatScreen> {
  late final TextEditingController _title = TextEditingController(
    text: widget.title,
  );

  _Cadence _cadence = _Cadence.weekly;
  int _every = 1;
  final Set<int> _weekdays = {DateTime.now().weekday};
  late int _dayOfMonth = DateTime.now().day;
  int _afterDays = 7;
  late DateTime _startsOn = DateTime.now();
  final Set<String> _rotation = {};

  bool _saving = false;

  @override
  void dispose() {
    _title.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    return Scaffold(
      appBar: AppBar(title: Text(l10n.repeatTitle)),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.fromLTRB(
            Space.gutter,
            0,
            Space.gutter,
            Space.air,
          ),
          children: [
            TextField(
              controller: _title,
              autofocus: widget.title.isEmpty,
              textCapitalization: TextCapitalization.sentences,
              style: theme.textTheme.bodyLarge,
              decoration: InputDecoration(hintText: l10n.repeatWhat),
            ),
            const SizedBox(height: Space.room),

            _Cadences(
              chosen: _cadence,
              onChosen: (cadence) => setState(() => _cadence = cadence),
            ),
            const SizedBox(height: Space.wide),

            ..._detail(l10n, theme),

            const SizedBox(height: Space.wide),
            _Label(l10n.repeatStartsOn),
            _DateRow(
              date: _startsOn,
              onPick: (picked) => setState(() => _startsOn = picked),
            ),

            const SizedBox(height: Space.wide),
            _Label(l10n.repeatRotation),
            Text(l10n.repeatRotationHint, style: theme.textTheme.bodySmall),
            const SizedBox(height: Space.snug),
            _Rotation(
              db: widget.db,
              householdId: widget.series.householdId,
              chosen: _rotation,
              onToggle: (id) => setState(() {
                if (!_rotation.remove(id)) {
                  _rotation.add(id);
                }
              }),
            ),

            const SizedBox(height: Space.air),
            FilledButton(
              onPressed: _saving ? null : _save,
              child: Text(l10n.repeatSave),
            ),
          ],
        ),
      ),
    );
  }

  /// The one control that changes with the cadence, and nothing else moves.
  List<Widget> _detail(AppLocalizations l10n, ThemeData theme) {
    switch (_cadence) {
      case _Cadence.daily:
        return [_EveryStepper(
          label: l10n.repeatEveryDays(_every),
          value: _every,
          onChanged: (n) => setState(() => _every = n),
        )];

      case _Cadence.weekly:
        return [
          _EveryStepper(
            label: l10n.repeatEveryWeeks(_every),
            value: _every,
            onChanged: (n) => setState(() => _every = n),
          ),
          const SizedBox(height: Space.gap),
          _Label(l10n.repeatOnDays),
          _Weekdays(
            chosen: _weekdays,
            onToggle: (day) => setState(() {
              if (!_weekdays.remove(day)) {
                _weekdays.add(day);
              }
              // Never none: a weekly chore with no day has no date to land on.
              if (_weekdays.isEmpty) {
                _weekdays.add(day);
              }
            }),
          ),
        ];

      case _Cadence.monthly:
        return [
          _EveryStepper(
            label: l10n.repeatEveryMonths(_every),
            value: _every,
            onChanged: (n) => setState(() => _every = n),
          ),
          const SizedBox(height: Space.gap),
          _EveryStepper(
            label: l10n.repeatDayOfMonth(_dayOfMonth),
            value: _dayOfMonth,
            max: 31,
            onChanged: (n) => setState(() => _dayOfMonth = n),
          ),
        ];

      case _Cadence.afterDone:
        return [
          _EveryStepper(
            label: l10n.repeatDaysAfter(_afterDays),
            value: _afterDays,
            max: 180,
            onChanged: (n) => setState(() => _afterDays = n),
          ),
        ];
    }
  }

  Repeat get _repeat {
    switch (_cadence) {
      case _Cadence.daily:
        return Repeat.daily(every: _every);
      case _Cadence.weekly:
        return Repeat.weekly(weekdays: _weekdays, every: _every);
      case _Cadence.monthly:
        return Repeat.monthly(dayOfMonth: _dayOfMonth, every: _every);
      case _Cadence.afterDone:
        return Repeat.after(days: _afterDays);
    }
  }

  void _save() {
    final title = _title.text.trim();
    if (title.isEmpty) {
      return;
    }

    setState(() => _saving = true);
    unawaited(
      widget.series
          .create(
            title: title,
            repeat: _repeat,
            startsOn: _startsOn,
            rotation: _rotation.toList(),
          )
          .then((_) {
            if (mounted) {
              Navigator.of(context).pop();
            }
          }),
    );
  }
}

class _Cadences extends StatelessWidget {
  const _Cadences({required this.chosen, required this.onChosen});

  final _Cadence chosen;
  final ValueChanged<_Cadence> onChosen;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    final labels = {
      _Cadence.daily: l10n.repeatDaily,
      _Cadence.weekly: l10n.repeatWeekly,
      _Cadence.monthly: l10n.repeatMonthly,
      _Cadence.afterDone: l10n.repeatAfterDone,
    };

    return Wrap(
      spacing: Space.snug,
      runSpacing: Space.snug,
      children: [
        for (final entry in labels.entries)
          ChoiceChip(
            label: Text(entry.value),
            selected: chosen == entry.key,
            onSelected: (_) => onChosen(entry.key),
          ),
      ],
    );
  }
}

/// The days of the week, named by the locale rather than by us.
class _Weekdays extends StatelessWidget {
  const _Weekdays({required this.chosen, required this.onToggle});

  final Set<int> chosen;
  final ValueChanged<int> onToggle;

  @override
  Widget build(BuildContext context) {
    final locale = Localizations.localeOf(context).toLanguageTag();
    // A known Monday, so the names come out in the order of the week whatever
    // the locale thinks the week starts on.
    final monday = DateTime(2026, 10, 26);

    // The narrow form — L M M J V S D — so all seven fit on one row of a
    // phone. At "lun. mar. mer." they wrapped after five, which reads as a
    // list that ran out of room rather than as a week.
    final format = DateFormat('EEEEE', locale);

    // A row of seven equal columns rather than a wrap. A week has seven days
    // and they are the same size as each other; counting pixels to make them
    // fit left the seventh on a line of its own, which reads as a list that
    // ran out of room instead of as a week.
    return Row(
      children: [
        for (var day = 0; day < 7; day++)
          Expanded(
            child: Padding(
              padding: EdgeInsets.only(right: day == 6 ? 0 : Space.tight),
              child: ChoiceChip(
                label: Center(
                  child: Text(format.format(monday.add(Duration(days: day)))),
                ),
                labelPadding: EdgeInsets.zero,
                padding: const EdgeInsets.symmetric(vertical: Space.snug),
                selected: chosen.contains(day + 1),
                // No tick: on a single letter the wash already says "chosen",
                // and the glyph would cost the width this row does not have.
                showCheckmark: false,
                onSelected: (_) => onToggle(day + 1),
              ),
            ),
          ),
      ],
    );
  }
}

/// A number with two large targets, rather than a free text field that would
/// invite "every 0 days".
class _EveryStepper extends StatelessWidget {
  const _EveryStepper({
    required this.label,
    required this.value,
    required this.onChanged,
    this.max = 12,
  });

  final String label;
  final int value;
  final int max;
  final ValueChanged<int> onChanged;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: Text(label, style: Theme.of(context).textTheme.bodyLarge),
        ),
        IconButton(
          onPressed: value > 1 ? () => onChanged(value - 1) : null,
          icon: const Icon(Icons.remove),
        ),
        IconButton(
          onPressed: value < max ? () => onChanged(value + 1) : null,
          icon: const Icon(Icons.add),
        ),
      ],
    );
  }
}

class _DateRow extends StatelessWidget {
  const _DateRow({required this.date, required this.onPick});

  final DateTime date;
  final ValueChanged<DateTime> onPick;

  @override
  Widget build(BuildContext context) {
    final locale = Localizations.localeOf(context).toLanguageTag();

    return InkWell(
      onTap: () => unawaited(_pick(context)),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: Space.step),
        child: Row(
          children: [
            Expanded(
              child: Text(
                DateFormat.yMMMMEEEEd(locale).format(date),
                style: Theme.of(context).textTheme.bodyLarge,
              ),
            ),
            const Icon(Icons.edit_calendar_outlined, size: 20),
          ],
        ),
      ),
    );
  }

  Future<void> _pick(BuildContext context) async {
    final picked = await showDatePicker(
      context: context,
      initialDate: date,
      firstDate: DateTime(date.year - 1),
      lastDate: DateTime(date.year + 5),
    );
    if (picked != null) {
      onPick(picked);
    }
  }
}

/// Who takes a turn, read from the local replica like everything else.
class _Rotation extends StatefulWidget {
  const _Rotation({
    required this.db,
    required this.householdId,
    required this.chosen,
    required this.onToggle,
  });

  final AppDatabase db;
  final String householdId;
  final Set<String> chosen;
  final ValueChanged<String> onToggle;

  @override
  State<_Rotation> createState() => _RotationState();
}

class _RotationState extends State<_Rotation> {
  late final Stream<List<Member>> _members =
      (widget.db.select(widget.db.members)
            ..where((m) => m.householdId.equals(widget.householdId))
            ..where((m) => m.deletedRev.isNull())
            ..orderBy([(m) => OrderingTerm.asc(m.rev)]))
          .watch();

  @override
  Widget build(BuildContext context) {
    return StreamBuilder<List<Member>>(
      stream: _members,
      builder: (context, snapshot) {
        final members = snapshot.data ?? const <Member>[];

        return Wrap(
          spacing: Space.snug,
          runSpacing: Space.snug,
          children: [
            for (final member in members)
              FilterChip(
                label: Text(_nameOf(member)),
                selected: widget.chosen.contains(member.id),
                onSelected: (_) => widget.onToggle(member.id),
              ),
          ],
        );
      },
    );
  }

  static String _nameOf(Member member) {
    final fields = jsonDecode(member.fields) as Map<String, dynamic>;
    final name = fields['display_name'] as String? ?? '';
    return name.isEmpty ? member.id : name;
  }
}

class _Label extends StatelessWidget {
  const _Label(this.text);

  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: Space.tight),
      child: Text(text, style: Theme.of(context).textTheme.labelSmall),
    );
  }
}
