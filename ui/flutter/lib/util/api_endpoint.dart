import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as path;

import 'util.dart';

/// 写出引擎 REST 端点，供浏览器 host 直连（与 cmd/server.go 约定一致）。
/// 仅 desktop + tcp 网络时写入；unix socket 路径 host 暂不消费。
Future<void> writeAPIEndpointFile({
  required String network,
  required String address,
  required int port,
}) async {
  if (!Util.isDesktop()) return;
  if (network != 'tcp') return;
  if (port <= 0) return;

  try {
    final home = await _homeDir();
    if (home == null) return;
    final dir = path.join(home, '.gopeed');
    await Directory(dir).create(recursive: true);

    var host = address.split(':').first;
    if (host.isEmpty || host == '0.0.0.0' || host == '::') {
      host = '127.0.0.1';
    }
    final hostPort = '$host:$port';
    final payload = jsonEncode({
      'address': hostPort,
      'url': 'http://$hostPort',
    });
    await File(path.join(dir, 'api-endpoint.json')).writeAsString(payload);
  } catch (e) {
    // 静默失败：host 会回退 Flutter 管道
  }
}

/// 应用退出时清理端点文件，避免 host 连到已死端口。
Future<void> clearAPIEndpointFile() async {
  if (!Util.isDesktop()) return;
  try {
    final home = await _homeDir();
    if (home == null) return;
    final f = File(path.join(home, '.gopeed', 'api-endpoint.json'));
    if (await f.exists()) {
      await f.delete();
    }
  } catch (_) {}
}

Future<String?> _homeDir() async {
  if (Platform.environment['USERPROFILE']?.isNotEmpty == true) {
    return Platform.environment['USERPROFILE'];
  }
  if (Platform.environment['HOME']?.isNotEmpty == true) {
    return Platform.environment['HOME'];
  }
  return null;
}
