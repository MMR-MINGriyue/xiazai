import 'package:flutter/material.dart';
import 'package:get/get.dart';
import 'package:window_manager/window_manager.dart';

import '../../../../api/model/task.dart';
import '../../../../util/util.dart';
import '../../../services/traffic_service.dart';
import '../../task/controllers/task_downloading_controller.dart';

/// IDM 式悬浮进度窗：桌面端将主窗缩为置顶小条，显示全局速度与总体进度。
/// 再次切换可恢复原窗口尺寸。
class FloatWindowController extends GetxController {
  final enabled = false.obs;

  Size? _savedSize;
  Offset? _savedPos;

  Future<void> toggle() async {
    if (!Util.isDesktop()) return;
    if (enabled.value) {
      await _restore();
    } else {
      await _enterFloat();
    }
  }

  Future<void> _enterFloat() async {
    try {
      final bounds = await windowManager.getBounds();
      _savedSize = bounds.size;
      _savedPos = bounds.topLeft;

      await windowManager.setAlwaysOnTop(true);
      await windowManager.setSize(const Size(320, 96));
      await windowManager.setAlignment(Alignment.topRight);
      await windowManager.setSkipTaskbar(true);
      // 保持可拖动（titleBarStyle 默认即可）
      enabled.value = true;
    } catch (e) {
      enabled.value = false;
    }
  }

  Future<void> _restore() async {
    try {
      await windowManager.setAlwaysOnTop(false);
      await windowManager.setSkipTaskbar(false);
      if (_savedSize != null) {
        await windowManager.setSize(_savedSize!);
      }
      if (_savedPos != null) {
        await windowManager.setPosition(_savedPos!);
      }
      enabled.value = false;
    } catch (_) {
      enabled.value = false;
    }
  }
}

/// 悬浮窗内容：速度 + 运行任务数 + 总进度条 + 关闭按钮。
class FloatWindowOverlay extends StatelessWidget {
  const FloatWindowOverlay({super.key, required this.controller});

  final FloatWindowController controller;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: Obx(() {
        if (!controller.enabled.value) {
          return const SizedBox.shrink();
        }
        return _FloatBody(controller: controller);
      }),
    );
  }
}

class _FloatBody extends StatelessWidget {
  const _FloatBody({required this.controller});

  final FloatWindowController controller;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      margin: const EdgeInsets.all(6),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: theme.colorScheme.surface.withOpacity(0.96),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: theme.colorScheme.outlineVariant),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withOpacity(0.18),
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
                Icon(Icons.downloading, size: 16, color: theme.colorScheme.primary),
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
                InkWell(
                  onTap: () => controller.toggle(),
                  child: Icon(Icons.close, size: 16, color: theme.disabledColor),
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
    );
  }
}
