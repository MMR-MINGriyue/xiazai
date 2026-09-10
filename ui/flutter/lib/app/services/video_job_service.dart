import 'dart:async';

import 'package:get/get.dart';

import '../../api/api.dart';
import '../../api/model/task.dart';
import '../../api/model/video.dart';

/// 轮询视频 job，提供 taskId → Job 映射，供任务列表展示合并状态。
class VideoJobService extends GetxService {
  /// jobId → VideoJob
  final jobs = <String, VideoJob>{}.obs;

  /// taskId → jobId（从 job.taskIds 反查）
  final _taskToJob = <String, String>{};

  Timer? _timer;

  Future<VideoJobService> init() async {
    _timer = Timer.periodic(const Duration(seconds: 2), (_) => _refresh());
    unawaited(_refresh());
    return this;
  }

  @override
  void onClose() {
    _timer?.cancel();
    super.onClose();
  }

  Future<void> _refresh() async {
    try {
      final list = await listVideoJobs();
      final byId = <String, VideoJob>{};
      final taskMap = <String, String>{};
      for (final j in list) {
        byId[j.id] = j;
        for (final tid in j.taskIds) {
          taskMap[tid] = j.id;
        }
      }
      jobs.assignAll(byId);
      _taskToJob
        ..clear()
        ..addAll(taskMap);
    } catch (_) {
      // 网络/服务未就绪时静默
    }
  }

  /// 根据任务 labels 或 id 找到对应 job。
  VideoJob? jobForTask(Task task) {
    final labels = task.meta.req.labels;
    final jobId = labels?['video.jobId'];
    if (jobId != null && jobId.isNotEmpty) {
      return jobs[jobId];
    }
    final mapped = _taskToJob[task.id];
    if (mapped != null) {
      return jobs[mapped];
    }
    return null;
  }

  /// 任务是否为视频流水线子任务。
  static bool isVideoTask(Task task) {
    final labels = task.meta.req.labels;
    return labels != null && labels.containsKey('video.jobId');
  }

  /// 合并完成的临时下载任务应从列表隐藏（后端也会删任务，此处兜底）。
  bool shouldHideTask(Task task) {
    if (!isVideoTask(task)) return false;
    final job = jobForTask(task);
    return job != null && job.status == 'done';
  }

  /// 合并状态文案（空=非视频任务或无 job）。
  static String statusLabel(VideoJob? job) {
    if (job == null) return '';
    switch (job.status) {
      case 'pending':
        return 'videoJobPending';
      case 'downloading':
        return 'videoJobDownloading';
      case 'merging':
        return 'videoJobMerging';
      case 'done':
        return 'videoJobDone';
      case 'error':
        return 'videoJobError';
      default:
        return job.status;
    }
  }
}
