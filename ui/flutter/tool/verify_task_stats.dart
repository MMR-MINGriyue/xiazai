// 纯 Dart 验证 TaskStats 模型解析（dart run tool/verify_task_stats.dart）
// flutter test 在本机因 flutter_tester WebSocket 通信问题不可用，此脚本等效覆盖模型层。
import 'package:gopeed/api/model/task_stats.dart';

int _passed = 0;

void _check(bool cond, String name) {
  if (!cond) {
    print('FAIL: $name');
    throw StateError(name);
  }
  _passed++;
  print('PASS: $name');
}

void main() {
  // sftp/ftp：顶层数组形态
  final stats = TaskStats.fromJson([
    {'begin': 0, 'end': 524287, 'downloaded': 524288},
    {'begin': 524288, 'end': 1048575, 'downloaded': 0},
    {'begin': 1048576, 'end': 1572863, 'downloaded': 1048576},
  ]);
  _check(stats.hasChunks, 'sftp 形态识别为 chunks');
  _check(stats.chunks!.length == 3, 'chunks 数量');
  _check(stats.totalDownloaded == 1572864, 'sftp 总下载量');
  _check(stats.chunks![0].completed, '完成段判定');
  _check(!stats.chunks![1].completed, '未完成段判定');
  _check(stats.chunks![1].ratio == 0, '0 进度 ratio');
  _check((stats.chunks![2].ratio - 1048576 / 524288).abs() < 0.01,
      '部分进度 ratio');
  _check(!stats.hasConnections, 'sftp 无 connections');

  // http：对象形态
  final h = TaskStats.fromJson({
    'connections': [
      {'downloaded': 1024, 'completed': true, 'failed': false, 'retryTimes': 0},
      {'downloaded': 512, 'completed': false, 'failed': false, 'retryTimes': 1},
      {'downloaded': 0, 'completed': false, 'failed': true, 'retryTimes': 3},
    ],
  });
  _check(h.hasConnections, 'http 形态识别为 connections');
  _check(h.connections!.length == 3, 'connections 数量');
  _check(h.totalDownloaded == 1536, 'http 总下载量');
  _check(h.connections![0].completed && !h.connections![0].failed, '完成连接');
  _check(h.connections![2].failed && h.connections![2].retryTimes == 3,
      '失败连接 + 重试次数');
  _check(!h.hasChunks, 'http 无 chunks');

  // 未知形态
  final empty = TaskStats.fromJson(null);
  _check(!empty.hasChunks && !empty.hasConnections && empty.totalDownloaded == 0,
      '未知形态返回空');

  // 缺字段容错
  final loose = TaskStats.fromJson([
    {'begin': 0},
  ]);
  _check(loose.chunks![0].end == 0 && loose.chunks![0].length == 1,
      '缺字段容错');

  print('ALL $_passed CHECKS PASSED');
}
