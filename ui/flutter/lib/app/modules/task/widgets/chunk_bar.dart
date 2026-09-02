import 'package:flutter/material.dart';

import '../../../../api/model/task_stats.dart';

/// 分段进度水平条（IDM 式）：
/// - sftp/ftp：按 chunk 区间比例画色块（绿=完成 / 蓝=下载中 / 灰=等待）
/// - http：按连接下载量比例画块（绿=完成 / 红=失败 / 蓝=进行中）
class ChunkBar extends StatelessWidget {
  final TaskStats stats;
  final int totalSize;
  final double height;

  const ChunkBar({
    super.key,
    required this.stats,
    required this.totalSize,
    this.height = 12,
  });

  @override
  Widget build(BuildContext context) {
    if (stats.hasChunks) {
      return _buildChunks(context);
    }
    if (stats.hasConnections) {
      return _buildConnections(context);
    }
    return _empty();
  }

  Widget _buildChunks(BuildContext context) {
    final chunks = stats.chunks!;
    final scale = totalSize > 0 ? totalSize : 1;
    final blocks = <Widget>[];
    for (var i = 0; i < chunks.length; i++) {
      final c = chunks[i];
      final flex = (c.length * 1000 / scale).ceil().clamp(1, 100000);
      blocks.add(Expanded(
        flex: flex,
        child: Container(
          margin: const EdgeInsets.symmetric(horizontal: 0.5),
          decoration: BoxDecoration(
            color: _chunkColor(c),
            borderRadius: BorderRadius.circular(1),
          ),
        ),
      ));
    }
    return _bar(blocks);
  }

  Widget _buildConnections(BuildContext context) {
    final conns = stats.connections!;
    final scale = totalSize > 0 ? totalSize : 1;
    final blocks = <Widget>[];
    for (var i = 0; i < conns.length; i++) {
      final c = conns[i];
      final flex = (c.downloaded * 1000 / scale).ceil().clamp(1, 100000);
      blocks.add(Expanded(
        flex: flex,
        child: Container(
          margin: const EdgeInsets.symmetric(horizontal: 0.5),
          decoration: BoxDecoration(
            color: _connColor(c),
            borderRadius: BorderRadius.circular(1),
          ),
        ),
      ));
    }
    return _bar(blocks);
  }

  Widget _bar(List<Widget> blocks) {
    return SizedBox(
      height: height,
      child: ClipRRect(
        borderRadius: BorderRadius.circular(height / 2),
        child: Row(
          children: blocks.isEmpty
              ? [Expanded(child: Container(color: Colors.grey.shade300))]
              : blocks,
        ),
      ),
    );
  }

  Widget _empty() {
    return SizedBox(
      height: height,
      child: ClipRRect(
        borderRadius: BorderRadius.circular(height / 2),
        child: Container(color: Colors.grey.shade300),
      ),
    );
  }

  Color _chunkColor(ChunkStats c) {
    if (c.completed) return Colors.green.shade400;
    if (c.downloaded > 0) return Colors.blue.shade400;
    return Colors.grey.shade300;
  }

  Color _connColor(ConnStats c) {
    if (c.completed) return Colors.green.shade400;
    if (c.failed) return Colors.red.shade400;
    if (c.downloaded > 0) return Colors.blue.shade400;
    return Colors.grey.shade300;
  }
}
