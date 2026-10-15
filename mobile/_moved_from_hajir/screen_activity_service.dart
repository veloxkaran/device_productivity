import 'dart:io';

import 'package:device_info_plus/device_info_plus.dart';
import 'package:dio/dio.dart';
import 'package:flutter/services.dart';
import 'package:flutter_dotenv/flutter_dotenv.dart';
import 'package:hajir/core/app_settings/shared_pref.dart';

/// Mobile side of the Device Activity hub (the same server the desktop
/// `my-monitor` agent syncs to). Android only: the native
/// ScreenActivityService does the capturing and syncing.
class ScreenActivityStatus {
  const ScreenActivityStatus({
    this.running = false,
    this.connected = false,
    this.companyName = '',
    this.lastError = '',
    this.lastCaptureAt,
  });

  final bool running;
  final bool connected;
  final String companyName;
  final String lastError;
  final DateTime? lastCaptureAt;
}

class HubCompany {
  const HubCompany(this.id, this.name);
  final int id;
  final String name;
}

/// Thrown when the employee works for several companies and must pick one.
class ChooseCompanyException implements Exception {
  ChooseCompanyException(this.companies);
  final List<HubCompany> companies;
}

class ScreenActivityException implements Exception {
  ScreenActivityException(this.message);
  final String message;
  @override
  String toString() => message;
}

class ScreenActivityService {
  ScreenActivityService._();
  static final instance = ScreenActivityService._();

  static const _channel = MethodChannel('com.veloxlab.hajir/screen_activity');

  static bool get isSupported => Platform.isAndroid;

  String get hubUrl => (dotenv.env['HUB_URL'] ?? '').trim().replaceAll(RegExp(r'/+$'), '');

  Future<ScreenActivityStatus> status() async {
    if (!isSupported) return const ScreenActivityStatus();
    final m = await _channel.invokeMapMethod<String, dynamic>('status') ?? {};
    final last = (m['lastCaptureAt'] as num?)?.toInt() ?? 0;
    return ScreenActivityStatus(
      running: m['running'] == true,
      connected: m['connected'] == true,
      companyName: (m['companyName'] ?? '').toString(),
      lastError: (m['lastError'] ?? '').toString(),
      lastCaptureAt: last > 0 ? DateTime.fromMillisecondsSinceEpoch(last) : null,
    );
  }

  /// Registers this phone as a device for the signed-in employee, using their
  /// Hajir login token (same flow as the desktop app's "Sign in with Hajir").
  Future<void> connect({int? companyId}) async {
    if (hubUrl.isEmpty) {
      throw ScreenActivityException('Activity server is not configured (HUB_URL).');
    }
    String deviceName = 'Android phone';
    try {
      final info = await DeviceInfoPlugin().androidInfo;
      deviceName = '${info.manufacturer} ${info.model}'.trim();
    } catch (_) {}

    final dio = Dio(BaseOptions(
      connectTimeout: const Duration(seconds: 15),
      receiveTimeout: const Duration(seconds: 30),
      validateStatus: (_) => true,
    ));
    final Response res;
    try {
      res = await dio.post(
        '$hubUrl/api/device/register',
        data: {
          if (companyId != null) 'company_id': companyId,
          'device_name': deviceName,
          'platform': 'android',
        },
        options: Options(headers: {'Authorization': 'Bearer ${AppSettings().token}'}),
      );
    } on DioException {
      throw ScreenActivityException("Can't reach the activity server.");
    }
    final body = res.data is Map ? res.data as Map : const {};
    if (res.statusCode == 409) {
      final list = (body['companies'] as List? ?? [])
          .map((c) => HubCompany((c['id'] as num).toInt(), (c['name'] ?? '').toString()))
          .toList();
      throw ChooseCompanyException(list);
    }
    if (res.statusCode != 200) {
      throw ScreenActivityException((body['message'] ?? 'Could not connect this device.').toString());
    }
    final data = body['data'] as Map? ?? const {};
    await _channel.invokeMethod('configure', {
      'hubUrl': hubUrl,
      'deviceToken': data['token'],
      'companyName': (data['company_name'] ?? '').toString(),
    });
  }

  /// Shows Android's screen-capture consent and starts tracking.
  /// Returns false if the employee declined.
  Future<bool> start() async => await _channel.invokeMethod<bool>('start') ?? false;

  Future<void> stop() => _channel.invokeMethod('stop');

  Future<void> disconnect() => _channel.invokeMethod('disconnect');
}
