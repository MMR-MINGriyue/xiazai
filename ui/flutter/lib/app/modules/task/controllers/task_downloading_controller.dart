import 'package:get/get.dart';

import '../../../../api/model/task.dart';
import 'task_list_controller.dart';

/// 下载中列表状态筛选（IDM 式）
enum TaskStatusFilter {
  all,
  running,
  waiting,
  paused,
  error,
}

class TaskDownloadingController extends TaskListController {
  TaskDownloadingController()
      : super([
          Status.ready,
          Status.running,
          Status.pause,
          Status.wait,
          Status.error
        ], (a, b) {
          if (a.status == Status.running && b.status != Status.running) {
            return -1;
          } else if (a.status != Status.running && b.status == Status.running) {
            return 1;
          } else {
            return b.updatedAt.compareTo(a.updatedAt);
          }
        });

  final statusFilter = TaskStatusFilter.all.obs;

  void setFilter(TaskStatusFilter f) {
    statusFilter.value = f;
  }

  /// 按当前筛选条件过滤后的任务列表
  List<Task> get filteredTasks {
    final f = statusFilter.value;
    if (f == TaskStatusFilter.all) {
      return tasks;
    }
    return tasks.where((t) {
      switch (f) {
        case TaskStatusFilter.running:
          return t.status == Status.running;
        case TaskStatusFilter.waiting:
          return t.status == Status.wait || t.status == Status.ready;
        case TaskStatusFilter.paused:
          return t.status == Status.pause;
        case TaskStatusFilter.error:
          return t.status == Status.error;
        case TaskStatusFilter.all:
          return true;
      }
    }).toList();
  }
}
