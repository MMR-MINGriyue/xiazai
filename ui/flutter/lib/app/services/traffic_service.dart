import 'dart:async';

import 'package:get/get.dart';

import '../../api/model/task.dart';
import '../modules/task/controllers/task_downloading_controller.dart';

/// 今日累计下载流量（进程内统计，按自然日重置）。
/// 通过轮询运行中任务的 progress.downloaded 增量累加。
class TrafficService extends GetxService {
  final todayBytes = 0.obs;
  final _lastSample = <String, int>{};
  DateTime _day = DateTime.now();
  Timer? _timer;

  Future<TrafficService> init() async {
    _timer = Timer.periodic(const Duration(seconds: 1), (_) => _sample());
    return this;
  }

  @override
  void onClose() {
    _timer?.cancel();
    super.onClose();
  }

  void _sample() {
    final now = DateTime.now();
    if (now.year != _day.year ||
        now.month != _day.month ||
        now.day != _day.day) {
      _day = now;
      todayBytes.value = 0;
      _lastSample.clear();
    }
    if (!Get.isRegistered<TaskDownloadingController>()) {
      return;
    }
    final tasks = Get.find<TaskDownloadingController>().tasks;
    var delta = 0;
    final seen = <String>{};
    for (final Task t in tasks) {
      seen.add(t.id);
      final downloaded = t.progress.downloaded;
      final last = _lastSample[t.id];
      if (last != null && downloaded > last) {
        delta += downloaded - last;
      }
      _lastSample[t.id] = downloaded;
    }
    // 清理已消失任务的缓存
    _lastSample.removeWhere((id, _) => !seen.contains(id));
    if (delta > 0) {
      todayBytes.value += delta;
    }
  }
}
