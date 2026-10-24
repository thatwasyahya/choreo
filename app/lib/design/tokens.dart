import 'package:flutter/material.dart';

/// Every value the interface is allowed to use.
///
/// One file, because a design system that lives in nine widgets is nine design
/// systems. A screen that needs a colour, a size or a duration takes it from
/// here; a screen that invents one is a bug a test can catch.
///
/// ## The direction: warm paper
///
/// Choreo is a shared surface in a kitchen, not a dashboard. The specification
/// asks for something "as simple as a post-it on the fridge, as reliable as a
/// professional tool", and product principle 6 forbids making anybody feel
/// guilty. That rules out two things immediately: the dark, dense, saturated
/// look of a developer tool, and the badges, streaks and scoreboards of a
/// habit tracker.
///
/// So: a warm off-white page, generous air, hierarchy carried by TYPE rather
/// than by boxes, and one earthen accent used only where something is to be
/// done. Cards are the exception, not the rule — a list of chores does not
/// need eight rectangles to be read.
///
/// The references are real design languages rather than a generated average:
/// the paper and restraint of Notion's reading surfaces, the typographic
/// hierarchy Apple uses when it stops shouting, and the editorial serif that
/// keeps either from feeling like a form.
abstract final class Tone {
  // --- the page -------------------------------------------------------------

  /// Warm off-white, not #FFFFFF. Pure white under a kitchen light at night is
  /// a torch; paper has a temperature.
  static const Color paper = Color(0xFFFBF8F3);

  /// A sheet laid on the page: the create sheet, a card that must lift.
  static const Color sheet = Color(0xFFFFFDFA);

  /// The faint rule between rows. A hairline does the work eight borders were
  /// doing.
  static const Color rule = Color(0xFFE8E1D5);

  // --- the ink --------------------------------------------------------------

  /// Near-black with a brown cast, so text on paper reads warm rather than
  /// printed by a laser.
  static const Color ink = Color(0xFF231F1A);

  /// Secondary text: who ticked it, when it is due.
  static const Color inkSoft = Color(0xFF6B6256);

  /// Placeholders and disabled labels. Never used for information somebody
  /// needs — it does not meet AA against paper at small sizes.
  static const Color inkFaint = Color(0xFF9A9183);

  // --- the one accent -------------------------------------------------------

  /// Terracotta. Earthen, domestic, and warm without being a toy. It marks
  /// the thing to do and nothing else: an accent used everywhere marks
  /// nothing.
  static const Color accent = Color(0xFFA6522E);

  /// For a surface tinted by the accent — a pressed row, a selected chip.
  static const Color accentWash = Color(0xFFF6EAE2);

  /// Done. Deliberately quiet: completing a chore is normal, not a
  /// celebration, and a bright green tick turns a household into a scoreboard.
  static const Color done = Color(0xFF5B6E4F);

  /// Something needs a decision — the review tray. Amber, not red: nothing in
  /// this product is an alarm.
  static const Color notice = Color(0xFFB07B2A);

  /// A destructive confirmation, and only that.
  static const Color danger = Color(0xFF8E3B2F);

  // --- the night ------------------------------------------------------------

  /// Warm charcoal rather than black: the same room with the lights off.
  static const Color paperNight = Color(0xFF1A1714);
  static const Color sheetNight = Color(0xFF241F1B);
  static const Color ruleNight = Color(0xFF3A332C);
  static const Color inkNight = Color(0xFFF2EBE0);
  static const Color inkSoftNight = Color(0xFFB8AE9F);
  static const Color inkFaintNight = Color(0xFF8A8073);

  /// Lifted, because terracotta at its daylight value goes muddy on charcoal.
  static const Color accentNight = Color(0xFFE09A6F);
  static const Color accentWashNight = Color(0xFF3A2A21);
  static const Color doneNight = Color(0xFF9DB08F);
  static const Color noticeNight = Color(0xFFE0B055);
  static const Color dangerNight = Color(0xFFE08273);
}

/// The two typefaces, bundled rather than fetched.
///
/// An app whose argument is that it works in a cellar must not download its
/// own letters. Both are OFL, and their licences sit next to the files.
abstract final class Fonts {
  /// Fraunces, an old-style serif with a bit of wonk. It carries the headings
  /// and the numbers that matter, and it is the single strongest reason this
  /// does not look like every other task app.
  static const String display = 'Fraunces';

  /// Atkinson Hyperlegible, drawn by the Braille Institute so that letters
  /// cannot be mistaken for one another at small sizes. It reads at arm's
  /// length across a kitchen, which is where this app is used — and choosing
  /// it is the same decision as principle 6, made in type.
  static const String body = 'AtkinsonHyperlegible';
}

/// The spacing scale: multiples of four, named for what they are for.
///
/// Values outside the scale are how a layout drifts into "almost aligned",
/// which the eye reads as sloppiness long before it can name it.
abstract final class Space {
  static const double hair = 2;
  static const double tight = 4;
  static const double snug = 8;
  static const double step = 12;
  static const double gap = 16;
  static const double wide = 24;
  static const double room = 32;
  static const double air = 48;

  /// The page gutter. Generous on purpose: air is most of what makes a surface
  /// feel calm, and it costs nothing.
  static const double gutter = 20;
}

/// Corner radii. Soft, never pill-shaped: a fully rounded row looks like a
/// toy, and a square one looks like a spreadsheet.
abstract final class Radii {
  static const Radius small = Radius.circular(8);
  static const Radius medium = Radius.circular(12);
  static const Radius large = Radius.circular(18);

  static const BorderRadius card = BorderRadius.all(medium);
  static const BorderRadius sheet = BorderRadius.vertical(top: large);
  static const BorderRadius field = BorderRadius.all(small);
}

/// Motion. Short, and with a curve that decelerates: an interface that takes
/// its time feels slow, and one that eases in both directions feels soft.
abstract final class Motion {
  /// A state change under the finger: a tick, a press.
  static const Duration quick = Duration(milliseconds: 120);

  /// Something arriving or leaving: a sheet, a row.
  static const Duration enter = Duration(milliseconds: 220);

  /// A whole screen.
  static const Duration page = Duration(milliseconds: 280);

  static const Curve ease = Curves.easeOutCubic;
}

/// The one size that is not a matter of taste.
///
/// Forty-eight device pixels is the floor for anything a finger has to hit,
/// and it is an accessibility requirement rather than a preference. A row that
/// looks elegant at 36 is a row somebody's parent cannot tick.
abstract final class Touch {
  static const double min = 48;
}
