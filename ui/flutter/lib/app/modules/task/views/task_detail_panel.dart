import 'dart:async';

import 'package:flutter/material.dart';

import '../../../../api/api.dart';
import '../../../../api/model/task.dart';
import '../../../../api/model/task_stats.dart';
import '../../../../util/util.dart';
import '../widgets/chunk_bar.dart';

/// 任务详情分段可视化面板（IDM 式）：
/// 500ms 轮询 GET /api/v1/tasks/{id}/stats，渲染分段进度条 + 连接明细。
/// 仅 running 状态轮询；暂停/完成/切换任务自动停止。
class TaskDetailPanel extends StatefulWidget {
  final Task? task;

  const TaskDetailPanel({super.key, this.task});

  @override
  State<TaskDetailPanel> createState() => _TaskDetailPanelState();
}

class _TaskDetailPanelState extends State<TaskDetailPanel> {
  TaskStats? _stats;
  Timer? _timer;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _timer = Timer.periodic(const Duration(milliseconds: 500), (_) => _poll());
  }

  @override
  void didUpdateWidget(covariant TaskDetailPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    // 切换任务后立即刷新一次
    if (oldWidget.task?.id != widget.task?.id) {
      _stats = null;
      _poll();
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _poll() async {
    final task = widget.task;
    if (task == null || _loading) return;
    // 仅下载中轮询；完成后拉最后一次
    if (task.status != Status.running && _stats != null) return;
    _loading = true;
    try {
      final s = await fetchTaskStats(task.id);
      if (mounted && s != null) {
        setState(() => _stats = s);
      }
    } finally {
      _loading = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final task = widget.task;
    if (task == null) {
      return const SizedBox.shrink();
    }
    final totalSize = task.meta.res?.size ?? 0;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Divider(height: 1),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: Text(
            '分段进度',
            style: Theme.of(context).textTheme.titleMedium,
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: _stats == null
              ? const Center(
                  child: Padding(
                    padding: EdgeInsets.all(8),
                    child: SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                  ),
                )
              : ChunkBar(stats: _stats!, totalSize: totalSize, height: 14),
        ),
        if (_stats != null) _buildDetail(context, task),
      ],
    );
  }

  Widget _buildDetail(BuildContext context, Task task) {
    final stats = _stats!;
    final TextStyle? muted = Theme.of(context)
        .textTheme
        .bodySmall
        ?.copyWith(color: Theme.of(context).disabledColor);

    if (stats.hasChunks) {
      final chunks = stats.chunks!;
      var done = 0;
      for (final c in chunks) {
        if (c.completed) done++;
      }
      return Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              '分段 ${chunks.length} 个 · 完成 $done · 已下载 ${Util.fmtByte(stats.totalDownloaded)}',
              style: muted,
            ),
            const SizedBox(height: 4),
            Wrap(
              spacing: 8,
              runSpacing: 4,
              children: [
                _legend(context, Colors.green.shade400, '完成'),
                _legend(context, Colors.blue.shade400, '下载中'),
                _legend(context, Colors.grey.shade300, '等待'),
              ],
            ),
          ],
        ),
      );
    }
    if (stats.hasConnections) {
      final conns = stats.connections!;
      return Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
        child: Text(
          '连接 ${conns.length} 个 · 已下载 ${Util.fmtByte(stats.totalDownloaded)}',
          style: muted,
        ),
      );
    }
    return const SizedBox.shrink();
  }

  Widget _legend(BuildContext context, Color color, String label) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 10,
          height: 10,
          decoration: BoxDecoration(
            color: color,
            borderRadius: BorderRadius.circular(2),
          ),
        ),
        const SizedBox(width: 4),
        Text(label, style: Theme.of(context).textTheme.bodySmall),
      ],
    );
  }
}
