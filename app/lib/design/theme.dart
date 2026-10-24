import 'package:choreo/design/tokens.dart';
import 'package:flutter/material.dart';

/// The theme, built once from the tokens.
///
/// Material is the machinery underneath — gestures, ripples, accessibility
/// plumbing, text selection — but none of its defaults survive here. A screen
/// that looks like stock Material 3 with a seed colour is a screen that looks
/// like every other Flutter app, which is precisely the complaint this answers.
///
/// The type scale is the load-bearing part. Hierarchy comes from size, weight
/// and family rather than from boxes and colour, so these numbers are the
/// difference between a page and a form.
abstract final class ChoreoTheme {
  static ThemeData get light => _build(
    brightness: Brightness.light,
    paper: Tone.paper,
    sheet: Tone.sheet,
    rule: Tone.rule,
    ink: Tone.ink,
    inkSoft: Tone.inkSoft,
    inkFaint: Tone.inkFaint,
    accent: Tone.accent,
    accentWash: Tone.accentWash,
    danger: Tone.danger,
  );

  static ThemeData get dark => _build(
    brightness: Brightness.dark,
    paper: Tone.paperNight,
    sheet: Tone.sheetNight,
    rule: Tone.ruleNight,
    ink: Tone.inkNight,
    inkSoft: Tone.inkSoftNight,
    inkFaint: Tone.inkFaintNight,
    accent: Tone.accentNight,
    accentWash: Tone.accentWashNight,
    danger: Tone.dangerNight,
  );

  static ThemeData _build({
    required Brightness brightness,
    required Color paper,
    required Color sheet,
    required Color rule,
    required Color ink,
    required Color inkSoft,
    required Color inkFaint,
    required Color accent,
    required Color accentWash,
    required Color danger,
  }) {
    final scheme = ColorScheme(
      brightness: brightness,
      primary: accent,
      onPrimary: brightness == Brightness.light ? Tone.sheet : Tone.inkNight,
      primaryContainer: accentWash,
      onPrimaryContainer: ink,
      secondary: inkSoft,
      onSecondary: paper,
      error: danger,
      onError: brightness == Brightness.light ? Tone.sheet : Tone.inkNight,
      errorContainer: accentWash,
      onErrorContainer: danger,
      surface: paper,
      onSurface: ink,
      surfaceContainerLowest: paper,
      surfaceContainerLow: paper,
      surfaceContainer: sheet,
      surfaceContainerHigh: sheet,
      surfaceContainerHighest: sheet,
      onSurfaceVariant: inkSoft,
      outline: rule,
      outlineVariant: rule,
    );

    final text = _typography(ink: ink, soft: inkSoft, faint: inkFaint);

    return ThemeData(
      useMaterial3: true,
      brightness: brightness,
      colorScheme: scheme,
      scaffoldBackgroundColor: paper,
      textTheme: text,
      fontFamily: Fonts.body,

      // Flat. An elevated bar draws a line under the title and turns a page
      // into a screen; the title is already the biggest thing there.
      // A screen's name is a HEADING, not a label on a form, and it is the
      // same heading on every screen. Setting this per screen is how nine
      // pages end up with five sizes: the first draft of this design did
      // exactly that, and "À revoir" stayed small while "À faire" grew.
      appBarTheme: AppBarTheme(
        backgroundColor: paper,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        scrolledUnderElevation: 0,
        centerTitle: false,
        titleTextStyle: text.headlineMedium,
        toolbarHeight: 88,
        titleSpacing: Space.gutter,
        iconTheme: IconThemeData(color: inkSoft, size: 22),
        actionsPadding: const EdgeInsets.only(right: Space.snug),
      ),

      dividerTheme: DividerThemeData(
        color: rule,
        thickness: 1,
        space: 1,
      ),

      // Cards are the exception here, not the rule, so they are quiet: a
      // hairline instead of a shadow, and no tint.
      cardTheme: CardThemeData(
        color: sheet,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        margin: EdgeInsets.zero,
        shape: RoundedRectangleBorder(
          borderRadius: Radii.card,
          side: BorderSide(color: rule),
        ),
      ),

      bottomSheetTheme: BottomSheetThemeData(
        backgroundColor: sheet,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        shape: const RoundedRectangleBorder(borderRadius: Radii.sheet),
        showDragHandle: true,
        dragHandleColor: rule,
      ),

      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          backgroundColor: accent,
          foregroundColor: brightness == Brightness.light
              ? Tone.sheet
              : Tone.paperNight,
          minimumSize: const Size.fromHeight(Touch.min),
          shape: const RoundedRectangleBorder(borderRadius: Radii.field),
          textStyle: text.labelLarge,
          elevation: 0,
        ),
      ),

      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          foregroundColor: ink,
          minimumSize: const Size.fromHeight(Touch.min),
          side: BorderSide(color: rule),
          shape: const RoundedRectangleBorder(borderRadius: Radii.field),
          textStyle: text.labelLarge,
        ),
      ),

      textButtonTheme: TextButtonThemeData(
        style: TextButton.styleFrom(
          foregroundColor: accent,
          minimumSize: const Size(Touch.min, Touch.min),
          textStyle: text.labelLarge,
        ),
      ),

      iconButtonTheme: IconButtonThemeData(
        style: IconButton.styleFrom(
          foregroundColor: inkSoft,
          minimumSize: const Size.square(Touch.min),
        ),
      ),

      // Underlined rather than boxed: a filled box per field makes a form, and
      // a line under the words keeps the eye on what was typed.
      inputDecorationTheme: InputDecorationTheme(
        filled: false,
        contentPadding: const EdgeInsets.symmetric(vertical: Space.step),
        hintStyle: text.bodyLarge?.copyWith(color: inkFaint),
        labelStyle: text.bodyMedium?.copyWith(color: inkSoft),
        helperStyle: text.bodySmall?.copyWith(color: inkSoft),
        helperMaxLines: 3,
        errorStyle: text.bodySmall?.copyWith(color: danger),
        enabledBorder: UnderlineInputBorder(
          borderSide: BorderSide(color: rule),
        ),
        focusedBorder: UnderlineInputBorder(
          borderSide: BorderSide(color: accent, width: 2),
        ),
        errorBorder: UnderlineInputBorder(
          borderSide: BorderSide(color: danger),
        ),
      ),

      checkboxTheme: CheckboxThemeData(
        fillColor: WidgetStateProperty.resolveWith((states) {
          return states.contains(WidgetState.selected) ? Tone.done : paper;
        }),
        checkColor: WidgetStateProperty.all(
          brightness == Brightness.light ? Tone.sheet : Tone.paperNight,
        ),
        side: BorderSide(color: inkFaint, width: 1.5),
        shape: const RoundedRectangleBorder(
          borderRadius: BorderRadius.all(Radius.circular(6)),
        ),
        materialTapTargetSize: MaterialTapTargetSize.padded,
      ),

      listTileTheme: ListTileThemeData(
        iconColor: inkSoft,
        titleTextStyle: text.bodyLarge,
        subtitleTextStyle: text.bodySmall?.copyWith(color: inkSoft),
        contentPadding: const EdgeInsets.symmetric(
          horizontal: Space.gutter,
          vertical: Space.tight,
        ),
        minVerticalPadding: Space.step,
      ),

      floatingActionButtonTheme: FloatingActionButtonThemeData(
        backgroundColor: accent,
        foregroundColor: brightness == Brightness.light
            ? Tone.sheet
            : Tone.paperNight,
        elevation: 2,
        focusElevation: 2,
        hoverElevation: 2,
        highlightElevation: 2,
        shape: const RoundedRectangleBorder(borderRadius: Radii.card),
        extendedTextStyle: text.labelLarge,
      ),

      snackBarTheme: SnackBarThemeData(
        backgroundColor: ink,
        contentTextStyle: text.bodyMedium?.copyWith(color: paper),
        behavior: SnackBarBehavior.floating,
        shape: const RoundedRectangleBorder(borderRadius: Radii.field),
      ),

      progressIndicatorTheme: ProgressIndicatorThemeData(
        color: accent,
        linearTrackColor: rule,
        circularTrackColor: rule,
      ),

      pageTransitionsTheme: const PageTransitionsTheme(
        builders: {
          TargetPlatform.android: CupertinoPageTransitionsBuilder(),
          TargetPlatform.linux: FadeUpwardsPageTransitionsBuilder(),
        },
      ),
    );
  }

  /// The scale.
  ///
  /// Two families with one job each: the serif names things, the sans says
  /// them. Mixing them per-widget is how a design loses its nerve, so the
  /// split lives here and screens just take a style.
  static TextTheme _typography({
    required Color ink,
    required Color soft,
    required Color faint,
  }) {
    TextStyle display(double size, {double weight = 400, double? height}) {
      return TextStyle(
        fontFamily: Fonts.display,
        fontSize: size,
        height: height,
        color: ink,
        fontWeight: FontWeight.values[(weight ~/ 100) - 1],
        letterSpacing: -0.2,
      );
    }

    TextStyle body(
      double size, {
      FontWeight weight = FontWeight.w400,
      Color? colour,
      double height = 1.4,
      double spacing = 0,
    }) {
      return TextStyle(
        fontFamily: Fonts.body,
        fontSize: size,
        height: height,
        color: colour ?? ink,
        fontWeight: weight,
        letterSpacing: spacing,
      );
    }

    return TextTheme(
      // The household's name, the welcome. Serif, large, and never shouted in
      // capitals.
      displaySmall: display(40, height: 1.1),
      headlineLarge: display(32, height: 1.15),

      // A screen's title: "À faire", "Les membres".
      headlineMedium: display(26, height: 1.2),
      titleLarge: display(22, weight: 500, height: 1.2),

      // A section inside a screen.
      titleMedium: body(16, weight: FontWeight.w700),
      titleSmall: body(14, weight: FontWeight.w700, colour: soft),

      // A task's title. Seventeen rather than sixteen: this is the line people
      // actually read, from across a room.
      bodyLarge: body(17, height: 1.35),
      bodyMedium: body(15),

      // Who ticked it, when it is due.
      bodySmall: body(13, colour: soft, height: 1.3),

      labelLarge: body(16, weight: FontWeight.w700),
      labelMedium: body(13, weight: FontWeight.w700, colour: soft),

      // The one place small capitals are allowed: a column header or a status,
      // where the letterspacing does the work a border would.
      labelSmall: body(
        11,
        weight: FontWeight.w700,
        colour: faint,
        spacing: 0.8,
      ),
    );
  }
}
