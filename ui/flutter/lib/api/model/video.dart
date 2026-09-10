/// 视频站流水线模型（对应 /api/v1/video/*）。
class VideoFormatOption {
  final String id;
  final String label;
  final int height;
  final int width;
  final String ext;
  final String videoUrl;
  final String audioUrl;
  final int size;

  VideoFormatOption({
    required this.id,
    required this.label,
    required this.height,
    required this.width,
    required this.ext,
    required this.videoUrl,
    required this.audioUrl,
    required this.size,
  });

  factory VideoFormatOption.fromJson(Map<String, dynamic> json) =>
      VideoFormatOption(
        id: json['id'] as String? ?? '',
        label: json['label'] as String? ?? '',
        height: (json['height'] as num?)?.toInt() ?? 0,
        width: (json['width'] as num?)?.toInt() ?? 0,
        ext: json['ext'] as String? ?? '',
        videoUrl: json['videoUrl'] as String? ?? '',
        audioUrl: json['audioUrl'] as String? ?? '',
        size: (json['size'] as num?)?.toInt() ?? 0,
      );
}

class VideoInfo {
  final String id;
  final String title;
  final String thumbnail;
  final double duration;
  final String webpageUrl;
  final List<VideoFormatOption> formats;

  VideoInfo({
    required this.id,
    required this.title,
    required this.thumbnail,
    required this.duration,
    required this.webpageUrl,
    required this.formats,
  });

  factory VideoInfo.fromJson(Map<String, dynamic> json) => VideoInfo(
        id: json['id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        thumbnail: json['thumbnail'] as String? ?? '',
        duration: (json['duration'] as num?)?.toDouble() ?? 0,
        webpageUrl: json['webpageUrl'] as String? ?? '',
        formats: (json['formats'] as List? ?? [])
            .whereType<Map<String, dynamic>>()
            .map(VideoFormatOption.fromJson)
            .toList(),
      );
}

class VideoJob {
  final String id;
  final String title;
  final String status;
  final String? error;
  final String outputPath;
  final List<String> taskIds;

  VideoJob({
    required this.id,
    required this.title,
    required this.status,
    this.error,
    required this.outputPath,
    required this.taskIds,
  });

  factory VideoJob.fromJson(Map<String, dynamic> json) => VideoJob(
        id: json['id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        status: json['status'] as String? ?? '',
        error: json['error'] as String?,
        outputPath: json['outputPath'] as String? ?? '',
        taskIds: (json['taskIds'] as List? ?? []).cast<String>(),
      );
}

class VideoBinariesInfo {
  final bool ready;
  final List<String> missing;
  final String ytDlp;
  final String ffmpeg;

  VideoBinariesInfo({
    required this.ready,
    required this.missing,
    required this.ytDlp,
    required this.ffmpeg,
  });

  factory VideoBinariesInfo.fromJson(Map<String, dynamic> json) =>
      VideoBinariesInfo(
        ready: json['ready'] as bool? ?? false,
        missing: (json['missing'] as List? ?? []).cast<String>(),
        ytDlp: json['ytDlp'] as String? ?? '',
        ffmpeg: json['ffmpeg'] as String? ?? '',
      );
}

/// 客户端识别视频站（与后端 videosvc.IsVideoURL 对齐，快速提示用）。
bool looksLikeVideoUrl(String url) {
  final u = url.trim().toLowerCase();
  const hosts = [
    'bilibili.com',
    'b23.tv',
    'youtube.com',
    'youtu.be',
    'acfun.cn',
    'v.qq.com',
    'iqiyi.com',
    'vimeo.com',
    'douyin.com',
    'ixigua.com',
  ];
  final uri = Uri.tryParse(u);
  if (uri == null || !uri.hasScheme) return false;
  final host = uri.host;
  if (host.isEmpty) return false;
  return hosts.any((h) => host == h || host.endsWith('.$h'));
}
