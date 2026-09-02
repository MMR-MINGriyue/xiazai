import 'package:flutter_test/flutter_test.dart';

import 'package:gopeed/api/model/task_stats.dart';

void main() {
  group('TaskStats.fromJson', () {
    test('sftp/ftp 顶层数组形态（chunks）', () {
      final stats = TaskStats.fromJson([
        {'begin': 0, 'end': 524287, 'downloaded': 524288},
        {'begin': 524288, 'end': 1048575, 'downloaded': 0},
        {'begin': 1048576, 'end': 1572863, 'downloaded': 1048576},
      ]);
      expect(stats.hasChunks, isTrue);
      expect(stats.chunks, hasLength(3));
      expect(stats.totalDownloaded, 1572864);
      expect(stats.chunks![0].completed, isTrue);
      expect(stats.chunks![1].completed, isFalse);
      expect(stats.chunks![1].ratio, 0);
      expect(stats.chunks![2].ratio, closeTo(0.667, 0.01));
      expect(stats.hasConnections, isFalse);
    });

    test('http 对象形态（connections）', () {
      final stats = TaskStats.fromJson({
        'connections': [
          {'downloaded': 1024, 'completed': true, 'failed': false, 'retryTimes': 0},
          {'downloaded': 512, 'completed': false, 'failed': false, 'retryTimes': 1},
          {'downloaded': 0, 'completed': false, 'failed': true, 'retryTimes': 3},
        ],
      });
      expect(stats.hasConnections, isTrue);
      expect(stats.connections, hasLength(3));
      expect(stats.totalDownloaded, 1536);
      expect(stats.connections![0].completed, isTrue);
      expect(stats.connections![2].failed, isTrue);
      expect(stats.connections![2].retryTimes, 3);
      expect(stats.hasChunks, isFalse);
    });

    test('未知形态返回空', () {
      final stats = TaskStats.fromJson(null);
      expect(stats.hasChunks, isFalse);
      expect(stats.hasConnections, isFalse);
      expect(stats.totalDownloaded, 0);
    });

    test('缺字段容错', () {
      final stats = TaskStats.fromJson([
        {'begin': 0},
      ]);
      expect(stats.chunks![0].end, 0);
      expect(stats.chunks![0].downloaded, 0);
      expect(stats.chunks![0].length, 1);
    });
  });
}
