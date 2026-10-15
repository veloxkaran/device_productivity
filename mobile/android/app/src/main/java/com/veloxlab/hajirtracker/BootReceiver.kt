package com.veloxlab.hajirtracker

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import android.util.Log

/**
 * Restarts tracking after the phone is powered on (or the app is updated).
 * Only configured devices act. The service comes up in "waiting" mode and shows
 * a one-tap resume notification; on MDM-managed devices where capture consent is
 * granted by policy, it resumes capturing with no tap.
 */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        val action = intent?.action ?: return
        if (action != Intent.ACTION_BOOT_COMPLETED &&
            action != Intent.ACTION_LOCKED_BOOT_COMPLETED &&
            action != "android.intent.action.QUICKBOOT_POWERON" &&
            action != Intent.ACTION_MY_PACKAGE_REPLACED
        ) return

        val prefs = ScreenActivityService.prefs(context)
        if (prefs.getString("device_token", "").isNullOrEmpty()) return

        Log.i("BootReceiver", "boot: starting tracker service")
        val svc = Intent(context, ScreenActivityService::class.java)
        try {
            if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(svc) else context.startService(svc)
        } catch (e: Exception) {
            Log.e("BootReceiver", "could not start service on boot", e)
        }
    }
}
