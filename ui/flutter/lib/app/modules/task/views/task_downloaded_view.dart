import 'package:flutter/material.dart';
import 'package:get/get.dart';

import '../../../views/buid_task_list_view.dart';
import '../controllers/task_downloaded_controller.dart';

class TaskDownloadedView extends GetView<TaskDownloadedController> {
  const TaskDownloadedView({Key? key}) : super(key: key);

  @override
  Widget build(BuildContext context) {
    return Obx(() {
      // 读取 RxList，列表刷新时重建
      final tasks = controller.tasks.toList();
      return BuildTaskListView(
          tasks: tasks, selectedTaskIds: controller.selectedTaskIds);
    });
  }
}
