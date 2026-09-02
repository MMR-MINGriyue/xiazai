/// 任务分段进度模型（GET /api/v1/tasks/{id}/stats）。
///
/// 后端按协议返回两种形态：
/// - http:  `{"connections": [{"downloaded":N,"completed":b,"failed":b,"retryTimes":n}]}`
/// - sftp/ftp: 顶层数组 `[{"begin":n,"end":n,"downloaded":n}]`（chunk 区间进度）
/// 前端按协议/形态自适应，供分段可视化（ChunkBar）消费。
class TaskStats {
  final List<ChunkStats>? chunks;
  final List<ConnStats>? connections;

  TaskStats({this.chunks, this.connections});

  bool get hasChunks => chunks != null && chunks!.isNotEmpty;
  bool get hasConnections => connections != null && connections!.isNotEmpty;

  /// 已下载总字节（http: 连接下载量之和；sftp/ftp: chunk 下载量之和）
  int get totalDownloaded {
    var sum = 0;
    if (chunks != null) {
      for (final c in chunks!) {
        sum += c.downloaded;
      }
    }
    if (connections != null) {
      for (final c in connections!) {
        sum += c.downloaded;
      }
    }
    return sum;
  }

  factory TaskStats.fromJson(dynamic json) {
    if (json is List) {
      // sftp/ftp：顶层 chunk 数组
      return TaskStats(
        chunks: json
            .map((e) => ChunkStats.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
    }
    if (json is Map<String, dynamic>) {
      return TaskStats(
        chunks: (json['chunks'] as List?)
            ?.map((e) => ChunkStats.fromJson(e as Map<String, dynamic>))
            .toList(),
        connections: (json['connections'] as List?)
            ?.map((e) => ConnStats.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
    }
    return TaskStats();
  }
}

/// sftp/ftp 的分段区间进度（[begin, end] 闭区间）
class ChunkStats {
  final int begin;
  final int end;
  final int downloaded;

  ChunkStats({required this.begin, required this.end, required this.downloaded});

  factory ChunkStats.fromJson(Map<String, dynamic> json) => ChunkStats(
        begin: json['begin'] as int? ?? 0,
        end: json['end'] as int? ?? 0,
        downloaded: json['downloaded'] as int? ?? 0,
      );

  int get length => end - begin + 1;

  bool get completed => downloaded >= length;

  /// 完成度 0..1
  double get ratio => length == 0 ? 0 : downloaded / length;
}

/// http 连接的下载进度（动态分段无固定区间）
class ConnStats {
  final int downloaded;
  final bool completed;
  final bool failed;
  final int retryTimes;

  ConnStats({
    required this.downloaded,
    required this.completed,
    required this.failed,
    required this.retryTimes,
  });

  factory ConnStats.fromJson(Map<String, dynamic> json) => ConnStats(
        downloaded: json['downloaded'] as int? ?? 0,
        completed: json['completed'] as bool? ?? false,
        failed: json['failed'] as bool? ?? false,
        retryTimes: json['retryTimes'] as int? ?? 0,
      );
}
