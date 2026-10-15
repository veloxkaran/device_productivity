import 'dart:async';

import 'package:flutter/material.dart';
import 'package:hajir/app/data/services/screen_activity_service.dart';
import 'package:hajir/app/routers/routers.dart';
import 'package:intl/intl.dart';

/// Employee screen to connect this phone to the Device Activity hub and
/// start/stop screenshot tracking (Android only).
class ScreenActivityPage extends StatefulWidget {
  const ScreenActivityPage({super.key});

  static Route<void> route() => MaterialPageRoute(
        settings: const RouteSettings(name: Routers.screenActivityPage),
        builder: (_) => const ScreenActivityPage(),
      );

  @override
  State<ScreenActivityPage> createState() => _ScreenActivityPageState();
}

class _ScreenActivityPageState extends State<ScreenActivityPage> with WidgetsBindingObserver {
  final _svc = ScreenActivityService.instance;
  ScreenActivityStatus _status = const ScreenActivityStatus();
  bool _busy = false;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _refresh();
    _poll = Timer.periodic(const Duration(seconds: 5), (_) => _refresh());
  }

  @override
  void dispose() {
    _poll?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _refresh();
  }

  Future<void> _refresh() async {
    if (!ScreenActivityService.isSupported) return;
    final s = await _svc.status();
    if (mounted) setState(() => _status = s);
  }

  void _toast(String msg) =>
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));

  Future<void> _run(Future<void> Function() job) async {
    setState(() => _busy = true);
    try {
      await job();
    } on ScreenActivityException catch (e) {
      _toast(e.message);
    } catch (e) {
      _toast('Something went wrong: $e');
    } finally {
      if (mounted) setState(() => _busy = false);
      await _refresh();
    }
  }

  Future<void> _connect([int? companyId]) => _run(() async {
        try {
          await _svc.connect(companyId: companyId);
        } on ChooseCompanyException catch (e) {
          final picked = await showDialog<int>(
            context: context,
            builder: (ctx) => SimpleDialog(
              title: const Text("Choose the company you're working for"),
              children: [
                for (final c in e.companies)
                  SimpleDialogOption(onPressed: () => Navigator.pop(ctx, c.id), child: Text(c.name)),
              ],
            ),
          );
          if (picked != null) await _svc.connect(companyId: picked);
        }
      });

  Future<void> _start() => _run(() async {
        final ok = await _svc.start();
        if (!ok) _toast('Screen capture permission was not granted.');
      });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(title: const Text('Activity Tracking')),
      body: !ScreenActivityService.isSupported
          ? const _Message(
              icon: Icons.phone_iphone,
              text: 'Screen activity tracking is available on Android only. '
                  'iPhone does not allow apps to capture the screen in the background.',
            )
          : ListView(
              padding: const EdgeInsets.all(16),
              children: [
                Card(
                  child: Padding(
                    padding: const EdgeInsets.all(16),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(children: [
                          Icon(Icons.circle,
                              size: 12,
                              color: _status.running
                                  ? Colors.green
                                  : _status.connected
                                      ? Colors.orange
                                      : Colors.grey),
                          const SizedBox(width: 8),
                          Text(
                            _status.running
                                ? 'Tracking'
                                : _status.connected
                                    ? 'Connected – not tracking'
                                    : 'Not connected',
                            style: theme.textTheme.titleMedium,
                          ),
                        ]),
                        if (_status.companyName.isNotEmpty) ...[
                          const SizedBox(height: 6),
                          Text('Company: ${_status.companyName}'),
                        ],
                        if (_status.lastCaptureAt != null) ...[
                          const SizedBox(height: 4),
                          Text('Last screenshot: ${DateFormat('hh:mm a').format(_status.lastCaptureAt!)}'),
                        ],
                        if (_status.lastError.isNotEmpty) ...[
                          const SizedBox(height: 4),
                          Text(_status.lastError, style: TextStyle(color: theme.colorScheme.error)),
                        ],
                      ],
                    ),
                  ),
                ),
                const SizedBox(height: 12),
                const Text(
                  'While tracking is on, Hajir takes screenshots of your phone screen at the interval '
                  'set by your employer and shares them, with your active/idle time, on the employer '
                  'activity dashboard. Android will ask for screen-capture permission each time you '
                  'start, and a notification stays visible until you stop. Screenshots are skipped '
                  'while the screen is off.',
                ),
                const SizedBox(height: 20),
                if (!_status.connected)
                  FilledButton.icon(
                    onPressed: _busy ? null : () => _connect(),
                    icon: const Icon(Icons.link),
                    label: const Text('Connect this phone'),
                  )
                else if (!_status.running)
                  FilledButton.icon(
                    onPressed: _busy ? null : _start,
                    icon: const Icon(Icons.play_arrow),
                    label: const Text('Start tracking'),
                  )
                else
                  FilledButton.icon(
                    style: FilledButton.styleFrom(backgroundColor: theme.colorScheme.error),
                    onPressed: _busy ? null : () => _run(_svc.stop),
                    icon: const Icon(Icons.stop),
                    label: const Text('Stop tracking'),
                  ),
                if (_status.connected && !_status.running) ...[
                  const SizedBox(height: 8),
                  TextButton(
                    onPressed: _busy ? null : () => _run(_svc.disconnect),
                    child: const Text('Disconnect this phone'),
                  ),
                ],
              ],
            ),
    );
  }
}

class _Message extends StatelessWidget {
  const _Message({required this.icon, required this.text});
  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) => Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(mainAxisSize: MainAxisSize.min, children: [
            Icon(icon, size: 48, color: Colors.grey),
            const SizedBox(height: 12),
            Text(text, textAlign: TextAlign.center),
          ]),
        ),
      );
}
