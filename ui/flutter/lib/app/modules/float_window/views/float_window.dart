import 'package:flutter/material.dart';
import 'package:get/get.dart';
import 'package:window_manager/window_manager.dart';

import '../../../../api/model/task.dart';
import '../../../../util/util.dart';
import '../../../services/traffic_service.dart';
import '../../task/controllers/task_downloading_controller.dart';

/// IDM 式悬浮进度窗：桌面端将主窗缩为置顶小条，显示全局速度与总体进度。
class FloatWindowController extends GetxController {
  final enabled = false.obs;

  Size? _savedSize;
  Offset? _savedPos;

  Future<void> toggle() async {
    if (!Util.isDesktop()) return;
    if (enabled.value) {
      await restore();
    } else {
      await enterFloat();
    }
  }

  Future<void> enterFloat() async {
    try {
      final bounds = await windowManager.getBounds();
      // 记住可恢复的尺寸；异常时用默认主窗尺寸
      if (bounds.width >= 400 && bounds.height >= 300) {
        _savedSize = bounds.size;
        _savedPos = bounds.topLeft;
      } else {
        _savedSize ??= const Size(1100, 720);
      }

      await windowManager.setAlwaysOnTop(true);
      await windowManager.setSkipTaskbar(true);
      await windowManager.setSize(const Size(360, 110));
      await windowManager.setAlignment(Alignment.topRight);
      enabled.value = true;
    } catch (e) {
      enabled.value = false;
    }
  }

  Future<void> restore() async {
    try {
      await windowManager.setAlwaysOnTop(false);
      await windowManager.setSkipTaskbar(false);
      final size = _savedSize ?? const Size(1100, 720);
      await windowManager.setSize(size);
      if (_savedPos != null) {
        await windowManager.setPosition(_savedPos!);
      } else {
        await windowManager.center();
      }
      enabled.value = false;
    } catch (_) {
      enabled.value = false;
    }
  }
}

/// 悬浮窗内容。未启用时完全不接收指针事件。
class FloatWindowOverlay extends StatelessWidget {
  const FloatWindowOverlay({super.key, required this.controller});

  final FloatWindowController controller;

  @override
  Widget build(BuildContext context) {
    return Obx(() {
      if (!controller.enabled.value) {
        // 必须 IgnorePointer，避免透明层挡住主界面点击
        return const IgnorePointer(
          child: SizedBox.expand(child: SizedBox.shrink()),
        );
      }
      return Positioned(
        top: 12,
        right: 12,
        child: _FloatBody(controller: controller),
      );
    });
  }
}

class _FloatBody extends StatelessWidget {
  const _FloatBody({required this.controller});

  final FloatWindowController controller;

  Future<void> _dragWindow() async {
    try {
      await windowManager.startDragging();
    } catch (_) {}
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Material(
      color: Colors.transparent,
      child: GestureDetector(
        onPanStart: (_) => _dragWindow(),
        onDoubleTap: () => controller.restore(),
        child: Container(
          width: 336,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            color: theme.colorScheme.surface.withOpacity(0.97),
            borderRadius: BorderRadius.circular(10),
            border: Border.all(color: theme.colorScheme.outlineVariant),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withOpacity(0.2),
                blurRadius: 12,
                offset: const Offset(0, 4),
              ),
            ],
          ),
          child: Obx(() {
            var speed = 0;
            var running = 0;
            var downloaded = 0;
            var total = 0;
            if (Get.isRegistered<TaskDownloadingController>()) {
              for (final Task t in Get.find<TaskDownloadingController>().tasks) {
                if (t.status == Status.running) {
                  speed += t.progress.speed;
                  running++;
                  downloaded += t.progress.downloaded;
                  total += t.meta.res?.size ?? 0;
                }
              }
            }
            var today = 0;
            if (Get.isRegistered<TrafficService>()) {
              today = Get.find<TrafficService>().todayBytes.value;
            }
            final progress =
                total > 0 ? (downloaded / total).clamp(0.0, 1.0) : 0.0;

            return Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(Icons.downloading,
                        size: 16, color: theme.colorScheme.primary),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        '${Util.fmtByte(speed)}/s',
                        style: theme.textTheme.titleSmall
                            ?.copyWith(fontWeight: FontWeight.w700),
                      ),
                    ),
                    Text(
                      running == 0 ? 'idle'.tr : '$running',
                      style: theme.textTheme.bodySmall
                          ?.copyWith(color: theme.disabledColor),
                    ),
                    const SizedBox(width: 4),
                    IconButton(
                      visualDensity: VisualDensity.compact,
                      iconSize: 16,
                      padding: EdgeInsets.zero,
                      constraints: const BoxConstraints(
                          minWidth: 28, minHeight: 28),
                      tooltip: 'floatWindowOff'.tr,
                      onPressed: () => controller.restore(),
                      icon: Icon(Icons.close, color: theme.disabledColor),
                    ),
                  ],
                ),
                const SizedBox(height: 6),
                ClipRRect(
                  borderRadius: BorderRadius.circular(3),
                  child: LinearProgressIndicator(
                    value: total > 0 ? progress : null,
                    minHeight: 4,
                  ),
                ),
                const SizedBox(height: 4),
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        total > 0
                            ? '${Util.fmtByte(downloaded)} / ${Util.fmtByte(total)}'
                            : 'noRunningTasks'.tr,
                        style: theme.textTheme.labelSmall
                            ?.copyWith(color: theme.disabledColor),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    Text(
                      Util.fmtByte(today),
                      style: theme.textTheme.labelSmall
                          ?.copyWith(color: theme.disabledColor),
                    ),
                  ],
                ),
              ],
            );
          }),
        ),
      ),
    );
  }
}
