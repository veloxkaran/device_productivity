package com.veloxlab.hajirtracker

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
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import android.util.Log
import androidx.core.app.NotificationCompat
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
        const val ACTION_STOP = "com.veloxlab.hajirtracker.STOP"
        val APP_VERSION = BuildConfig.VERSION_NAME

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
    private val frameLock = Any()
    private var lastFrame: Bitmap? = null

    @Volatile private var intervalSec = 600
    @Volatile private var quality = 60
    @Volatile private var maxWidth = 1080
    @Volatile private var trackingEnabled = true
    @Volatile private var clockInAt = 0L
    @Volatile private var lastInteractiveAt = SystemClock.elapsedRealtime()
    @Volatile private var nextShotAt = 0L
    private val samples = JSONArray() // guarded by synchronized(samples)
    // All hub HTTP runs here, OFF the capture thread, so a slow/offline network
    // never stalls screenshot capture (the earlier cause of dropped frames).
    private val net: ExecutorService = Executors.newSingleThreadExecutor()

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
        val code = intent?.getIntExtra(EXTRA_RESULT_CODE, Activity.RESULT_CANCELED) ?: Activity.RESULT_CANCELED
        @Suppress("DEPRECATION")
        val data: Intent? = if (Build.VERSION.SDK_INT >= 33)
            intent?.getParcelableExtra(EXTRA_RESULT_DATA, Intent::class.java)
        else intent?.getParcelableExtra(EXTRA_RESULT_DATA)

        // No fresh consent (first launch of the service, or a system restart after
        // the process was killed). A configured device waits in the foreground and
        // asks for the one tap to resume; it does NOT give up, so tracking survives
        // reboots and low-memory kills. We must NOT start a mediaProjection
        // foreground service here — Android 14+ rejects it without an active
        // projection — so enterWaiting() uses the dataSync type instead.
        if (code != Activity.RESULT_OK || data == null) {
            if (deviceToken.isEmpty()) { stopSelf(); return START_NOT_STICKY }
            enterWaiting()
            return START_STICKY
        }
        waiting = false
        if (running) return START_STICKY

        // Consent in hand: become a mediaProjection foreground service, then project.
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q)
                startForeground(NOTIFICATION_ID, notification(), ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)
            else startForeground(NOTIFICATION_ID, notification())
        } catch (e: Exception) {
            Log.e(TAG, "startForeground(mediaProjection) failed", e)
            enterWaiting()
            return START_STICKY
        }

        val mpm = getSystemService(Context.MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
        val mp = try { mpm.getMediaProjection(code, data) } catch (e: Exception) { null }
        if (mp == null) {
            lastError = "Could not start screen capture"
            enterWaiting()
            return START_STICKY
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
        Agent.rollDay(this)
        prefs(this).edit().putLong("open_clock_in", clockInAt).putLong("session_start", clockInAt).apply()
        nextShotAt = SystemClock.elapsedRealtime() + 5_000 // first shot shortly after start
        handler.post(heartbeatTick)
        handler.post(sampleTick)
        handler.post(captureTick)
        handler.postDelayed(syncTick, 3_000)
        return START_STICKY
    }

    @Volatile private var waiting = false

    /** Keep the service alive (foreground) with a tap-to-resume notification until
     *  the one screen-capture consent is granted again (after a reboot/kill). */
    private fun enterWaiting() {
        waiting = true
        running = false
        // Not capturing yet, so use DATA_SYNC (MEDIA_PROJECTION requires an active
        // projection on Android 14+ and would throw here).
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q)
                startForeground(NOTIFICATION_ID, resumeNotification(), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
            else startForeground(NOTIFICATION_ID, resumeNotification())
        } catch (e: Exception) { Log.e(TAG, "waiting startForeground failed", e) }
        // Still send heartbeats so the dashboard shows the device online (idle), and
        // reflects that screenshots are paused awaiting consent.
        handler.removeCallbacks(waitingHeartbeat)
        handler.post(waitingHeartbeat)
    }

    private val waitingHeartbeat = object : Runnable {
        override fun run() {
            if (!waiting) return
            lastError = "Waiting for screen-capture permission (tap the notification)"
            net.execute { sendHeartbeat("idle") }
            handler.postDelayed(this, HEARTBEAT_MS)
        }
    }

    // ── Capture ────────────────────────────────────────────────────

    private fun setupDisplay(mp: MediaProjection) {
        val m = resources.displayMetrics
        // Capture at reduced size; final resize to maxWidth happens on encode.
        val scale = if (m.widthPixels > 1080) 1080f / m.widthPixels else 1f
        val w = (m.widthPixels * scale).toInt().coerceAtLeast(1)
        val h = (m.heightPixels * scale).toInt().coerceAtLeast(1)
        reader = ImageReader.newInstance(w, h, PixelFormat.RGBA_8888, 2).apply {
            setOnImageAvailableListener({ rd ->
                val img = try { rd.acquireLatestImage() } catch (_: Exception) { null } ?: return@setOnImageAvailableListener
                // The virtual display fires on every frame; converting each one to a
                // Bitmap would churn CPU/GC for a screenshot taken only once per
                // interval. Keep a frame only when one is needed soon (or none yet),
                // otherwise just drain to keep the reader flowing.
                val need = synchronized(frameLock) { lastFrame == null } ||
                    SystemClock.elapsedRealtime() >= nextShotAt - 6_000
                if (!need) { img.close(); return@setOnImageAvailableListener }
                try {
                    val plane = img.planes[0]
                    val stride = plane.pixelStride
                    val rowPad = plane.rowStride - stride * img.width
                    val padded = Bitmap.createBitmap(img.width + rowPad / stride, img.height, Bitmap.Config.ARGB_8888)
                    padded.copyPixelsFromBuffer(plane.buffer)
                    val frame = if (rowPad == 0) padded else {
                        val cropped = Bitmap.createBitmap(padded, 0, 0, img.width, img.height)
                        padded.recycle(); cropped
                    }
                    synchronized(frameLock) { lastFrame?.recycle(); lastFrame = frame }
                } catch (e: Exception) {
                    Log.w(TAG, "frame decode failed: ${'$'}{e.message}")
                } finally { img.close() }
            }, handler)
        }
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
                if (trackingEnabled && !Agent.onBreak(this@ScreenActivityService) && isInteractive()) captureOnce()
                nextShotAt = now + intervalSec * 1000L
            }
            handler.postDelayed(this, 5_000)
        }
    }

    private fun captureOnce() {
        val src = synchronized(frameLock) { lastFrame }
        if (src == null || src.isRecycled) {
            lastError = "Waiting for first frame"
            return
        }
        try {
            var bmp = src
            if (bmp.width > maxWidth) {
                bmp = Bitmap.createScaledBitmap(src, maxWidth, (src.height * maxWidth.toFloat() / src.width).toInt(), true)
            }
            val out = ByteArrayOutputStream()
            bmp.compress(Bitmap.CompressFormat.JPEG, quality.coerceIn(20, 95), out)
            if (bmp !== src) bmp.recycle()
            val at = System.currentTimeMillis()
            File(queueDir, "$at.jpg").writeBytes(out.toByteArray())
            trimQueue()
            lastCaptureAt = at
            lastError = ""
        } catch (e: Exception) {
            Log.e(TAG, "capture failed", e)
            lastError = "Capture failed: ${'$'}{e.message}".take(250)
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
            if (trackingEnabled && !Agent.onBreak(this@ScreenActivityService)) {
                val row = JSONObject().put("is_active", interactive).put("idle_seconds", idle)
                    .put("app_name", "Android").put("captured_at", iso(System.currentTimeMillis()))
                synchronized(samples) { samples.put(row) }
            }
            handler.postDelayed(this, SYNC_MS)
        }
    }

    private val heartbeatTick = object : Runnable {
        override fun run() {
            if (!running) return
            val status = when {
                Agent.onBreak(this@ScreenActivityService) -> "break"
                isInteractive() -> "active"
                else -> "idle"
            }
            net.execute { sendHeartbeat(status) }
            handler.postDelayed(this, HEARTBEAT_MS)
        }
    }

    private val syncTick = object : Runnable {
        override fun run() {
            if (!running) return
            net.execute { syncAll() }
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
            if (d.has("tracking_mode")) prefs(this).edit().putBoolean("auto_mode", d.optString("tracking_mode") == "auto").apply()
        } catch (_: Exception) {}
    }

    private fun syncAll() {
        // Open time entry (keeps the session visible as "clocked in").
        if (clockInAt > 0) {
            val row = JSONObject().put("clock_in", iso(clockInAt)).put("clock_out", JSONObject.NULL)
            postJson("/api/sync/time-entries", JSONArray().put(row).toString())
        }
        val (payload, count) = synchronized(samples) {
            if (samples.length() == 0) null to 0 else samples.toString() to samples.length()
        }
        if (payload != null && postJson("/api/sync/activity", payload) != null) {
            synchronized(samples) { repeat(count) { if (samples.length() > 0) samples.remove(0) } }
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
        synchronized(frameLock) { lastFrame?.recycle(); lastFrame = null }
        try { reader?.close() } catch (_: Exception) {}
        try { projection?.stop() } catch (_: Exception) {}
        virtualDisplay = null; reader = null; projection = null
        net.execute {
            Agent.endBreakIfAny(this)
            Agent.closeSession(this)
            if (deviceToken.isNotEmpty()) {
                if (wasRunning) syncAll()
                closeOpenEntry()
                sendHeartbeat("offline")
            }
            clockInAt = 0
            handler.post { stopForeground(STOP_FOREGROUND_REMOVE); stopSelf() }
        }
        Log.i(TAG, "shutdown (userStopped=$userStopped)")
    }

    override fun onDestroy() {
        running = false
        net.shutdown()
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
            // IMPORTANCE_MIN: no sound, no heads-up, no status-bar icon — the
            // required foreground notification stays collapsed in the shade.
            val ch = NotificationChannel(CHANNEL_ID, "System service", NotificationManager.IMPORTANCE_MIN)
            ch.setShowBadge(false)
            ch.setSound(null, null)
            ch.enableVibration(false)
            ch.lockscreenVisibility = Notification.VISIBILITY_SECRET
            (getSystemService(NOTIFICATION_SERVICE) as NotificationManager).createNotificationChannel(ch)
        }
    }

    private fun silent(b: NotificationCompat.Builder) = b
        .setSmallIcon(android.R.drawable.stat_notify_sync)
        .setPriority(NotificationCompat.PRIORITY_MIN)
        .setCategory(NotificationCompat.CATEGORY_SERVICE)
        .setSilent(true)
        .setShowWhen(false)
        .setOngoing(true)
        .setContentIntent(openIntent())

    private fun openIntent() = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

    // Automatic-only device: the notification is required by Android while the
    // screen is being captured, but it carries no Stop control.
    private fun notification(): Notification =
        silent(NotificationCompat.Builder(this, CHANNEL_ID).setContentTitle("System service")).build()

    // Shown after a reboot/kill until the one screen-capture consent is re-granted.
    private fun resumeNotification(): Notification =
        silent(NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("Tap to resume")).build()
}
