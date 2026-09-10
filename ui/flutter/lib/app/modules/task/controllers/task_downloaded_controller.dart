import 'package:get/get.dart';
import 'package:gopeed/app/modules/task/controllers/task_list_controller.dart';

import '../../../../api/api.dart';
import '../../../../api/model/task.dart';
import '../../../services/video_job_service.dart';

class TaskDownloadedController extends TaskListController {
  TaskDownloadedController()
      : super([Status.done], (a, b) => b.updatedAt.compareTo(a.updatedAt));

  @override
  getTasksState() async {
    var list = await getTasks(statuses);
    // 合并完成的视频输出文件合成「已完成」条目
    if (Get.isRegistered<VideoJobService>()) {
      final svc = Get.find<VideoJobService>();
      final videoOut = svc.completedOutputTasks();
      final existingNames = list.map((t) => t.name).toSet();
      for (final t in videoOut) {
        // 避免与真实下载任务重名重复展示
        if (!existingNames.contains(t.name)) {
          list.add(t);
        }
      }
    }
    list.sort(compare);
    this.tasks.value = list;
  }
}
