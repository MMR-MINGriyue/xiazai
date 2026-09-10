import 'package:flutter/material.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:get/get.dart';
import 'package:window_manager/window_manager.dart';

import '../../../../i18n/message.dart';
import '../../../../theme/theme.dart';
import '../../../../util/locale_manager.dart';
import '../../../../util/util.dart';
import '../../../rpc/webview_rpc_overlay.dart';
import '../../../rpc/webview_rpc_service.dart';
import '../../../routes/app_pages.dart';
import '../../float_window/views/float_window.dart';
import '../controllers/app_controller.dart';

class AppView extends GetView<AppController> {
  const AppView({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    final config = controller.downloaderConfig.value;
    return WithForegroundTask(
      child: GetMaterialApp.router(
        useInheritedMediaQuery: true,
        debugShowCheckedModeBanner: false,
        theme: GopeedTheme.light,
        darkTheme: GopeedTheme.dark,
        themeMode: ThemeMode.values.byName(config.extra.themeMode),
        translations: messages,
        locale: toLocale(config.extra.locale),
        fallbackLocale: fallbackLocale,
        localizationsDelegates: const [
          GlobalMaterialLocalizations.delegate,
          GlobalWidgetsLocalizations.delegate,
          GlobalCupertinoLocalizations.delegate,
        ],
        supportedLocales: messages.keys.keys.map((e) => toLocale(e)).toList(),
        getPages: AppPages.routes,
        builder: (context, child) {
          if (Util.isDesktop()) {
            final brightness = Theme.of(context).brightness;
            windowManager.setBrightness(brightness);
          }
          // 稳定 Overlay：避免每次 rebuild 新建导致手势/点击失效
          return _StableOverlay(child: child ?? const SizedBox.shrink());
        },
      ),
    );
  }
}

/// 固定 Overlay 结构，子树变化只更新 entry，不销毁 Overlay。
class _StableOverlay extends StatefulWidget {
  const _StableOverlay({required this.child});

  final Widget child;

  @override
  State<_StableOverlay> createState() => _StableOverlayState();
}

class _StableOverlayState extends State<_StableOverlay> {
  late final OverlayEntry _appEntry;
  OverlayEntry? _webviewEntry;
  OverlayEntry? _floatEntry;

  @override
  void initState() {
    super.initState();
    _appEntry = OverlayEntry(builder: (_) => widget.child);
    if (WebViewRpcService.instance.supported) {
      _webviewEntry = OverlayEntry(builder: (_) => const WebViewRpcOverlay());
    }
    if (Util.isDesktop()) {
      _floatEntry = OverlayEntry(
        builder: (_) {
          final c = Get.isRegistered<FloatWindowController>()
              ? Get.find<FloatWindowController>()
              : FloatWindowController();
          return FloatWindowOverlay(controller: c);
        },
      );
    }
  }

  @override
  void didUpdateWidget(covariant _StableOverlay oldWidget) {
    super.didUpdateWidget(oldWidget);
    // 只刷新主内容 entry
    _appEntry.markNeedsBuild();
  }

  @override
  void dispose() {
    _appEntry.remove();
    _webviewEntry?.remove();
    _floatEntry?.remove();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final entries = <OverlayEntry>[_appEntry];
    if (_webviewEntry != null) entries.add(_webviewEntry!);
    if (_floatEntry != null) entries.add(_floatEntry!);
    return Overlay(initialEntries: entries);
  }
}
