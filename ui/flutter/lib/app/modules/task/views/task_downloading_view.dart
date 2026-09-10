import 'package:flutter/material.dart';
import 'package:get/get.dart';

import '../../../views/buid_task_list_view.dart';
import '../controllers/task_downloading_controller.dart';

class TaskDownloadingView extends GetView<TaskDownloadingController> {
  const TaskDownloadingView({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        _buildFilterBar(context),
        Expanded(
          child: Obx(() {
            // 读取 statusFilter 与 tasks，确保筛选/刷新都会重建
            final filtered = controller.filteredTasks.toList();
            return BuildTaskListView(
                tasks: filtered, selectedTaskIds: controller.selectedTaskIds);
          }),
        ),
      ],
    );
  }

  Widget _buildFilterBar(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: Obx(() {
        final current = controller.statusFilter.value;
        return Wrap(
          spacing: 8,
          runSpacing: 4,
          children: [
            _chip(context, theme, TaskStatusFilter.all, 'filterAll'.tr, current),
            _chip(context, theme, TaskStatusFilter.running, 'filterRunning'.tr,
                current),
            _chip(context, theme, TaskStatusFilter.waiting, 'filterWaiting'.tr,
                current),
            _chip(context, theme, TaskStatusFilter.paused, 'filterPaused'.tr,
                current),
            _chip(context, theme, TaskStatusFilter.error, 'filterError'.tr,
                current),
          ],
        );
      }),
    );
  }

  Widget _chip(
    BuildContext context,
    ThemeData theme,
    TaskStatusFilter value,
    String label,
    TaskStatusFilter current,
  ) {
    final selected = value == current;
    return FilterChip(
      label: Text(label, style: TextStyle(fontSize: 12)),
      selected: selected,
      showCheckmark: false,
      visualDensity: VisualDensity.compact,
      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
      padding: const EdgeInsets.symmetric(horizontal: 8),
      onSelected: (_) => controller.setFilter(value),
    );
  }
}
