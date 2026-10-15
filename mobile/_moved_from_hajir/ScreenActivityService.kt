package com.veloxlab.hajir

import android.app.Activity
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.Bitmap
import android.graphics.PixelFormat
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.ImageReader
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.IBinder
import android.os.PowerManager
import android.os.SystemClock
import android.util.Log
import androidx.core.app.NotificationCompat
import com.an4soft.hajir.MainActivity
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import java.util.UUID

/**
 * Mobile counterpart of the desktop `my-monitor` agent. It talks to the same
 * Device Activity Hub with the same device-token API, so screenshots, time
 * entries, activity samples and heartbeats show up on
 * /employer/dashboard/activity exactly like a desktop device.
 *
 * Android requires the user to approve screen capture (system dialog) every
 * time tracking starts, and a notification stays visible while it runs.
 */
class ScreenActivityService : Service() {
    companion object {
        private const val TAG = "ScreenActivityService"
        private const val CHANNEL_ID = "screen_activity_channel"
        private const val NOTIFICATION_ID = 9420
        const val PREFS = "hajir_screen_activity"
        const val EXTRA_RESULT_CODE = "result_code"
        const val EXTRA_RESULT_DATA = "result_data"
        const val ACTION_STOP = "com.veloxlab.hajir.SCREEN_ACTIVITY_STOP"
        const val APP_VERSION = "mobile-1.0.0"

        private const val HEARTBEAT_MS = 20_000L
        private const val SYNC_MS = 60_000L
        private const val MAX_QUEUE = 200

        @Volatile private var running = false
        @Volatile var lastError: String = ""
            private set
        @Volatile var lastCaptureAt: Long = 0L
            private set

        fun isRunning() = running

        fun prefs(ctx: Context) = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

        fun iso(ms: Long): String {
            val f = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US)
            f.timeZone = TimeZone.getTimeZone("UTC")
            return f.format(Date(ms))
        }
    }

    private lateinit var worker: HandlerThread
    private lateinit var handler: Handler
    private var projection: MediaProjection? = null
    private var virtualDisplay: VirtualDisplay? = null
    private var reader: ImageReader? = null

    private var intervalSec = 600
    private var quality = 60
    private var maxWidth = 1080
    private var trackingEnabled = true
    private var clockInAt = 0L
    private var lastInteractiveAt = SystemClock.elapsedRealtime()
    private var nextShotAt = 0L
    private val samples = JSONArray()

    private val hubUrl get() = prefs(this).getString("hub_url", "")!!.trimEnd('/')
    private val deviceToken get() = prefs(this).getString("device_token", "")!!
    private val queueDir get() = File(filesDir, "screen_activity_queue").apply { mkdirs() }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        createChannel()
        worker = HandlerThread("screen-activity").apply { start() }
        handler = Handler(worker.looper)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            shutdown(userStopped = true)
            return START_NOT_STICKY
        }
        // Must be in the foreground with type mediaProjection BEFORE getMediaProjection().
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                startForeground(NOTIFICATION_ID, notification(), ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)
            } else {
                startForeground(NOTIFICATION_ID, notification())
            }
        } catch (e: Exception) {
            Log.e(TAG, "startForeground failed", e)
            stopSelf()
            return START_NOT_STICKY
        }

        val code = intent?.getIntExtra(EXTRA_RESULT_CODE, Activity.RESULT_CANCELED) ?: Activity.RESULT_CANCELED
        @Suppress("DEPRECATION")
        val data: Intent? = if (Build.VERSION.SDK_INT >= 33)
            intent?.getParcelableExtra(EXTRA_RESULT_DATA, Intent::class.java)
        else intent?.getParcelableExtra(EXTRA_RESULT_DATA)

        // A restart without a fresh consent token (e.g. process killed) can't capture: stop cleanly.
        if (code != Activity.RESULT_OK || data == null || deviceToken.isEmpty()) {
            lastError = "Screen capture permission missing"
            shutdown(userStopped = false)
            return START_NOT_STICKY
        }
        if (running) return START_NOT_STICKY

        val mpm = getSystemService(Context.MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
        val mp = try { mpm.getMediaProjection(code, data) } catch (e: Exception) { null }
        if (mp == null) {
            lastError = "Could not start screen capture"
            shutdown(userStopped = false)
            return START_NOT_STICKY
        }
        projection = mp
        // Android 14+: callback must be registered before createVirtualDisplay.
        mp.registerCallback(object : MediaProjection.Callback() {
            override fun onStop() {
                Log.i(TAG, "projection stopped by system/user")
                handler.post { shutdown(userStopped = true) }
            }
        }, handler)
        setupDisplay(mp)

        running = true
        lastError = ""
        clockInAt = System.currentTimeMillis()
        prefs(this).edit().putLong("open_clock_in", clockInAt).apply()
        nextShotAt = SystemClock.elapsedRealtime() + 5_000 // first shot shortly after start
        handler.post(heartbeatTick)
        handler.post(sampleTick)
        handler.post(captureTick)
        handler.postDelayed(syncTick, 3_000)
        return START_NOT_STICKY
    }

    // ── Capture ────────────────────────────────────────────────────

    private fun setupDisplay(mp: MediaProjection) {
        val m = resources.displayMetrics
        // Capture at reduced size; final resize to maxWidth happens on encode.
        val scale = if (m.widthPixels > 1080) 1080f / m.widthPixels else 1f
        val w = (m.widthPixels * scale).toInt().coerceAtLeast(1)
        val h = (m.heightPixels * scale).toInt().coerceAtLeast(1)
        reader = ImageReader.newInstance(w, h, PixelFormat.RGBA_8888, 2)
        virtualDisplay = mp.createVirtualDisplay(
            "hajir-activity", w, h, m.densityDpi,
            DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR, reader!!.surface, null, handler
        )
    }

    private val captureTick = object : Runnable {
        override fun run() {
            if (!running) return
            val now = SystemClock.elapsedRealtime()
            if (now >= nextShotAt) {
                if (trackingEnabled && isInteractive()) captureOnce()
                nextShotAt = now + intervalSec * 1000L
            }
            handler.postDelayed(this, 5_000)
        }
    }

    private fun captureOnce() {
        val img = try { reader?.acquireLatestImage() } catch (e: Exception) { null }
        if (img == null) {
            lastError = "No frame available"
            return
        }
        try {
            val plane = img.planes[0]
            val pixelStride = plane.pixelStride
            val rowPadding = plane.rowStride - pixelStride * img.width
            val full = Bitmap.createBitmap(img.width + rowPadding / pixelStride, img.height, Bitmap.Config.ARGB_8888)
            full.copyPixelsFromBuffer(plane.buffer)
            var bmp = Bitmap.createBitmap(full, 0, 0, img.width, img.height)
            if (bmp != full) full.recycle()
            if (bmp.width > maxWidth) {
                val s = Bitmap.createScaledBitmap(bmp, maxWidth, (bmp.height * maxWidth.toFloat() / bmp.width).toInt(), true)
                bmp.recycle(); bmp = s
            }
            val out = ByteArrayOutputStream()
            bmp.compress(Bitmap.CompressFormat.JPEG, quality.coerceIn(20, 95), out)
            bmp.recycle()
            val at = System.currentTimeMillis()
            File(queueDir, "$at.jpg").writeBytes(out.toByteArray())
            trimQueue()
            lastCaptureAt = at
            lastError = ""
        } catch (e: Exception) {
            Log.e(TAG, "capture failed", e)
            lastError = "Capture failed: ${e.message}".take(250)
        } finally {
            img.close()
        }
    }

    private fun trimQueue() {
        val files = queueDir.listFiles()?.sortedBy { it.name } ?: return
        if (files.size > MAX_QUEUE) files.take(files.size - MAX_QUEUE).forEach { it.delete() }
    }

    private fun isInteractive(): Boolean =
        (getSystemService(Context.POWER_SERVICE) as PowerManager).isInteractive

    // ── Activity samples / heartbeat / sync ────────────────────────

    private val sampleTick = object : Runnable {
        override fun run() {
            if (!running) return
            val interactive = isInteractive()
            if (interactive) lastInteractiveAt = SystemClock.elapsedRealtime()
            val idle = (SystemClock.elapsedRealtime() - lastInteractiveAt) / 1000
            if (trackingEnabled) {
                samples.put(JSONObject().put("is_active", interactive).put("idle_seconds", idle)
                    .put("app_name", "Android").put("captured_at", iso(System.currentTimeMillis())))
            }
            handler.postDelayed(this, SYNC_MS)
        }
    }

    private val heartbeatTick = object : Runnable {
        override fun run() {
            if (!running) return
            sendHeartbeat(if (isInteractive()) "active" else "idle")
            handler.postDelayed(this, HEARTBEAT_MS)
        }
    }

    private val syncTick = object : Runnable {
        override fun run() {
            if (!running) return
            syncAll()
            handler.postDelayed(this, SYNC_MS)
        }
    }

    private fun sendHeartbeat(status: String) {
        val body = JSONObject()
            .put("status", status)
            .put("idle_seconds", (SystemClock.elapsedRealtime() - lastInteractiveAt) / 1000)
            .put("is_clocked_in", status != "offline")
            .put("app_name", "Android")
            .put("platform", "android")
            .put("app_version", APP_VERSION)
            .put("screenshot_interval_seconds", intervalSec)
            .put("capture_error", lastError)
            .put("last_capture_at", if (lastCaptureAt > 0) iso(lastCaptureAt) else "")
        val res = postJson("/api/heartbeat", body.toString()) ?: return
        try {
            val d = JSONObject(res).optJSONObject("data") ?: return
            d.optInt("screenshot_interval_seconds", 0).takeIf { it > 0 }?.let {
                if (it != intervalSec) {
                    intervalSec = it
                    nextShotAt = minOf(nextShotAt, SystemClock.elapsedRealtime() + it * 1000L)
                }
            }
            d.optInt("screenshot_quality", 0).takeIf { it > 0 }?.let { quality = it }
            d.optInt("screenshot_max_width", 0).takeIf { it > 0 }?.let { maxWidth = it }
            if (d.has("tracking_enabled")) trackingEnabled = d.optBoolean("tracking_enabled", true)
        } catch (_: Exception) {}
    }

    private fun syncAll() {
        // Open time entry (keeps the session visible as "clocked in").
        if (clockInAt > 0) {
            val row = JSONObject().put("clock_in", iso(clockInAt)).put("clock_out", JSONObject.NULL)
            postJson("/api/sync/time-entries", JSONArray().put(row).toString())
        }
        if (samples.length() > 0) {
            val payload = samples.toString()
            if (postJson("/api/sync/activity", payload) != null) {
                while (samples.length() > 0) samples.remove(0)
            }
        }
        uploadQueue()
    }

    private fun uploadQueue() {
        val files = queueDir.listFiles()?.sortedBy { it.name } ?: return
        for (f in files.take(20)) {
            val at = f.nameWithoutExtension.toLongOrNull()
            if (at == null) { f.delete(); continue }
            when (uploadShot(f, at)) {
                in 200..299, 400, 413, 422 -> f.delete() // done or permanently rejected
                else -> return // network/server issue: retry next sync
            }
        }
    }

    private fun closeOpenEntry() {
        val p = prefs(this)
        val open = p.getLong("open_clock_in", 0L)
        if (open <= 0) return
        val row = JSONObject().put("clock_in", iso(open)).put("clock_out", iso(System.currentTimeMillis()))
        if (postJson("/api/sync/time-entries", JSONArray().put(row).toString()) != null) {
            p.edit().remove("open_clock_in").apply()
        }
    }

    @Volatile private var stopping = false

    private fun shutdown(userStopped: Boolean) {
        if (stopping) return
        stopping = true
        val wasRunning = running
        running = false
        handler.removeCallbacksAndMessages(null)
        try { virtualDisplay?.release() } catch (_: Exception) {}
        try { reader?.close() } catch (_: Exception) {}
        try { projection?.stop() } catch (_: Exception) {}
        virtualDisplay = null; reader = null; projection = null
        handler.post {
            if (deviceToken.isNotEmpty()) {
                if (wasRunning) syncAll()
                closeOpenEntry()
                sendHeartbeat("offline")
            }
            clockInAt = 0
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
        Log.i(TAG, "shutdown (userStopped=$userStopped)")
    }

    override fun onDestroy() {
        running = false
        worker.quitSafely()
        super.onDestroy()
    }

    // ── HTTP ───────────────────────────────────────────────────────

    /** Returns the response body on 2xx, null otherwise. */
    private fun postJson(path: String, json: String): String? {
        if (hubUrl.isEmpty()) return null
        var c: HttpURLConnection? = null
        return try {
            c = (URL(hubUrl + path).openConnection() as HttpURLConnection).apply {
                requestMethod = "POST"; connectTimeout = 15_000; readTimeout = 30_000; doOutput = true
                setRequestProperty("Authorization", "Bearer $deviceToken")
                setRequestProperty("Content-Type", "application/json")
            }
            c.outputStream.use { it.write(json.toByteArray()) }
            if (c.responseCode == 401) onUnauthorized()
            if (c.responseCode in 200..299) c.inputStream.bufferedReader().readText() else null
        } catch (e: Exception) {
            Log.w(TAG, "POST $path failed: ${e.message}")
            null
        } finally { c?.disconnect() }
    }

    private fun uploadShot(f: File, at: Long): Int {
        if (hubUrl.isEmpty()) return -1
        val boundary = "----hajir" + UUID.randomUUID().toString().replace("-", "")
        var c: HttpURLConnection? = null
        return try {
            c = (URL("$hubUrl/api/sync/screenshots").openConnection() as HttpURLConnection).apply {
                requestMethod = "POST"; connectTimeout = 15_000; readTimeout = 60_000; doOutput = true
                setRequestProperty("Authorization", "Bearer $deviceToken")
                setRequestProperty("Content-Type", "multipart/form-data; boundary=$boundary")
            }
            c.outputStream.use { o ->
                fun w(s: String) = o.write(s.toByteArray())
                w("--$boundary\r\nContent-Disposition: form-data; name=\"captured_at\"\r\n\r\n${iso(at)}\r\n")
                w("--$boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"${f.name}\"\r\nContent-Type: image/jpeg\r\n\r\n")
                f.inputStream().use { it.copyTo(o) }
                w("\r\n--$boundary--\r\n")
            }
            val code = c.responseCode
            if (code == 401) onUnauthorized()
            code
        } catch (e: Exception) {
            Log.w(TAG, "screenshot upload failed: ${e.message}")
            -1
        } finally { c?.disconnect() }
    }

    private fun onUnauthorized() {
        // Device was removed by the employer (or token rotated). Disconnect.
        lastError = "Device was removed from the activity server"
        prefs(this).edit().remove("device_token").apply()
        if (running) handler.post { shutdown(userStopped = false) }
    }

    // ── Notification ───────────────────────────────────────────────

    private fun createChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val ch = NotificationChannel(CHANNEL_ID, "Activity tracking", NotificationManager.IMPORTANCE_LOW)
            ch.description = "Shown while Hajir records work activity and screenshots"
            (getSystemService(NOTIFICATION_SERVICE) as NotificationManager).createNotificationChannel(ch)
        }
    }

    private fun notification(): Notification {
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val stop = PendingIntent.getService(this, 1, Intent(this, ScreenActivityService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("Activity tracking is on")
            .setContentText("Hajir is recording work activity and screenshots for your employer.")
            .setSmallIcon(android.R.drawable.ic_menu_camera)
            .setOngoing(true)
            .setContentIntent(open)
            .addAction(0, "Stop", stop)
            .build()
    }
}
