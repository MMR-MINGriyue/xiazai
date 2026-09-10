import 'package:flutter/material.dart';
import 'package:get/get.dart';

import '../../../../api/model/task.dart';
import '../../../../util/util.dart';
import '../../../routes/app_pages.dart';
import '../../../services/traffic_service.dart';
import '../../../views/responsive_builder.dart';
import '../../app/controllers/app_controller.dart';
import '../../float_window/views/float_window.dart';
import '../../task/controllers/task_downloading_controller.dart';
import '../controllers/home_controller.dart';

class HomeView extends GetView<HomeController> {
  const HomeView({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    return GetRouterOutlet.builder(builder: (context, delegate, currentRoute) {
      switch (currentRoute?.uri.path) {
        case Routes.EXTENSION:
          controller.currentIndex.value = 1;
          break;
        case Routes.SETTING:
          controller.currentIndex.value = 2;
          break;
        default:
          controller.currentIndex.value = 0;
          break;
      }

      return Scaffold(
        // extendBody: true,
        body: Row(
            // crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              !ResponsiveBuilder.isNarrow(context)
                  ? NavigationRail(
                      extended: true,
                      labelType: NavigationRailLabelType.none,
                      minExtendedWidth: 170,
                      groupAlignment: 0,
                      // useIndicator: false,
                      onDestinationSelected: (int index) {
                        controller.currentIndex.value = index;
                        switch (index) {
                          case 0:
                            delegate.offAndToNamed(Routes.TASK);
                            break;
                          case 1:
                            delegate.offAndToNamed(Routes.EXTENSION);
                            break;
                          case 2:
                            delegate.offAndToNamed(Routes.SETTING);
                            break;
                        }
                      },
                      destinations: [
                        NavigationRailDestination(
                          icon: const Icon(Icons.task),
                          selectedIcon: const Icon(Icons.task),
                          label: Text('task'.tr),
                        ),
                        NavigationRailDestination(
                          icon: const Icon(Icons.extension),
                          selectedIcon: const Icon(Icons.extension),
                          label: Text('extensions'.tr),
                        ),
                        NavigationRailDestination(
                          icon: const Icon(Icons.settings),
                          selectedIcon: const Icon(Icons.settings),
                          label: Text('setting'.tr),
                        ),
                      ],
                      selectedIndex: controller.currentIndex.value,
                      leading: const Icon(Icons.menu),
                      // trailing: const Icon(Icons.info_outline),
                    )
                  : const SizedBox.shrink(),
              Expanded(
                  child: GetRouterOutlet(
                initialRoute: Routes.TASK,
                // anchorRoute: '/',
                // filterPages: (afterAnchor) {
                //   logger.w(afterAnchor);
                //   logger.w(afterAnchor.take(1));
                //   return afterAnchor.take(1);
                // },
              ))
            ]),
        bottomNavigationBar: ResponsiveBuilder.isNarrow(context)
            ? BottomNavigationBar(
                items: <BottomNavigationBarItem>[
                  BottomNavigationBarItem(
                    icon: const Icon(Icons.task),
                    label: 'task'.tr,
                  ),
                  BottomNavigationBarItem(
                    icon: const Icon(Icons.extension),
                    label: 'extensions'.tr,
                  ),
                  BottomNavigationBarItem(
                    icon: const Icon(Icons.settings),
                    label: 'setting'.tr,
                  ),
                ],
                currentIndex: controller.currentIndex.value,
                // selectedItemColor: Get.theme.highlightColor,
                onTap: (index) {
                  controller.currentIndex.value = index;
                  switch (index) {
                    case 0:
                      delegate.offAndToNamed(Routes.TASK);
                      break;
                    case 1:
                      delegate.offAndToNamed(Routes.EXTENSION);
                      break;
                    case 2:
                      delegate.offAndToNamed(Routes.SETTING);
                      break;
                  }
                },
              )
            : _buildGlobalSpeedBar(context),
      );
    });
  }

  /// 宽屏底部状态栏：全局下载速度 + 今日流量 + 限速提示 + 运行中任务数（IDM 式）
  Widget _buildGlobalSpeedBar(BuildContext context) {
    final theme = Theme.of(context);
    return Obx(() {
      var speed = 0;
      var running = 0;
      if (Get.isRegistered<TaskDownloadingController>()) {
        for (final t in Get.find<TaskDownloadingController>().tasks) {
          if (t.status == Status.running) {
            speed += t.progress.speed;
            running++;
          }
        }
      }
      var today = 0;
      if (Get.isRegistered<TrafficService>()) {
        today = Get.find<TrafficService>().todayBytes.value;
      }
      var rateLimit = 0;
      if (Get.isRegistered<AppController>()) {
        rateLimit =
            Get.find<AppController>().downloaderConfig.value.globalRateLimit;
      }
      return BottomAppBar(
        height: 40,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Row(
          children: [
            Icon(Icons.downloading,
                size: 18, color: theme.colorScheme.primary),
            const SizedBox(width: 8),
            Text(
              '${Util.fmtByte(speed)}/s',
              style: theme.textTheme.bodyMedium
                  ?.copyWith(fontWeight: FontWeight.w600),
            ),
            if (rateLimit > 0) ...[
              const SizedBox(width: 6),
              Tooltip(
                message: 'globalRateLimit'.tr,
                child: Icon(Icons.speed,
                    size: 14, color: theme.colorScheme.tertiary),
              ),
            ],
            const SizedBox(width: 16),
            Icon(Icons.today, size: 14, color: theme.disabledColor),
            const SizedBox(width: 4),
            Text(
              Util.fmtByte(today),
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.disabledColor),
            ),
            const Spacer(),
            Text(
              running == 0
                  ? 'noRunningTasks'.tr
                  : 'tasksRunning'.trParams({'count': running.toString()}),
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.disabledColor),
            ),
            if (Util.isDesktop() &&
                Get.isRegistered<FloatWindowController>()) ...[
              const SizedBox(width: 8),
              Builder(builder: (context) {
                // 在同一个外层 Obx 内读取，避免嵌套 Obx
                final floatOn = Get.find<FloatWindowController>().enabled.value;
                return IconButton(
                  visualDensity: VisualDensity.compact,
                  iconSize: 18,
                  tooltip: floatOn ? 'floatWindowOff'.tr : 'floatWindowOn'.tr,
                  icon: Icon(
                    floatOn
                        ? Icons.close_fullscreen
                        : Icons.picture_in_picture_alt,
                    color: floatOn
                        ? theme.colorScheme.primary
                        : theme.disabledColor,
                  ),
                  onPressed: () => Get.find<FloatWindowController>().toggle(),
                );
              }),
            ],
          ],
        ),
      );
    });
  }
}
