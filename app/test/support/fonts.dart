import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

/// Load the real typefaces into a test.
///
/// Without this, a widget test renders every string in Ahem — the blank
/// rectangle font — so a golden image would show the layout and nothing about
/// the type. Since the type is where this design's hierarchy lives, a golden
/// without the fonts would be a picture of the wrong thing.
Future<void> loadChoreoFonts() async {
  TestWidgetsFlutterBinding.ensureInitialized();

  const families = {
    'Fraunces': [
      'assets/fonts/Fraunces-400.ttf',
      'assets/fonts/Fraunces-500.ttf',
      'assets/fonts/Fraunces-600.ttf',
    ],
    'AtkinsonHyperlegible': [
      'assets/fonts/AtkinsonHyperlegible-400.ttf',
      'assets/fonts/AtkinsonHyperlegible-700.ttf',
    ],
  };

  for (final entry in families.entries) {
    final loader = FontLoader(entry.key);
    for (final path in entry.value) {
      final bytes = await File(path).readAsBytes();
      loader.addFont(Future.value(ByteData.sublistView(bytes)));
    }
    await loader.load();
  }

  await _loadIcons();
}

/// The icon font, from the SDK's own cache.
///
/// Without it every icon renders as an empty square — which a golden image
/// shows faithfully, and which made the first run of these look like a design
/// with holes in it rather than a harness with a gap in it.
Future<void> _loadIcons() async {
  final flutter = Platform.environment['FLUTTER_ROOT'] ??
      '${Platform.environment['HOME']}/flutter';
  final icons = File(
    '$flutter/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf',
  );
  if (!icons.existsSync()) {
    return;
  }

  final loader = FontLoader('MaterialIcons')
    ..addFont(Future.value(ByteData.sublistView(await icons.readAsBytes())));
  await loader.load();
}
