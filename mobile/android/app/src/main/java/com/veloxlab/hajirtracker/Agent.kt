package com.veloxlab.hajirtracker

import android.content.Context
import android.os.Build
import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * Android implementation of the desktop agent's local `/app/api/...` endpoints
 * (web/client.go). The bundled app.html is the desktop UI unchanged; its
 * fetch('/app/api/…') calls are routed here, so behaviour and responses match.
 */
object Agent {
    class ApiError(val status: Int, val body: JSONObject) : Exception(body.optString("error"))

    private fun err(status: Int, msg: String, extra: JSONObject? = null): Nothing =
        throw ApiError(status, (extra ?: JSONObject()).put("error", msg))

    private fun p(ctx: Context) = ScreenActivityService.prefs(ctx)
    private fun now() = System.currentTimeMillis()
    private fun today() = SimpleDateFormat("yyyy-MM-dd", Locale.US).format(Date())
    private fun iso(ms: Long) = ScreenActivityService.iso(ms)

    val hubUrl get() = BuildConfig.HUB_URL.trimEnd('/')
    val apiUrl get() = BuildConfig.HAJIR_API_URL.trimEnd('/')

    // ── Day / session bookkeeping (used by the service too) ─────────

    fun rollDay(ctx: Context) {
        val pr = p(ctx)
        if (pr.getString("day", "") != today()) {
            pr.edit().putString("day", today()).putLong("day_seconds", 0).putLong("break_day_seconds", 0).apply()
        }
    }

    fun onBreak(ctx: Context) = p(ctx).getLong("break_start", 0L) > 0

    fun closeSession(ctx: Context) {
        val pr = p(ctx)
        val start = pr.getLong("session_start", 0L)
        if (start <= 0) return
        rollDay(ctx)
        val add = ((now() - start) / 1000).coerceAtLeast(0)
        pr.edit().putLong("day_seconds", pr.getLong("day_seconds", 0) + add).remove("session_start").apply()
    }

    /** Ends an open break and syncs it. Blocking (network). */
    fun endBreakIfAny(ctx: Context) {
        val pr = p(ctx)
        val start = pr.getLong("break_start", 0L)
        if (start <= 0) return
        val end = now()
        rollDay(ctx)
        pr.edit().remove("break_start")
            .putLong("break_day_seconds", pr.getLong("break_day_seconds", 0) + (end - start) / 1000).apply()
        syncBreak(ctx, start, end)
    }

    private fun syncBreak(ctx: Context, start: Long, end: Long?) {
        val row = JSONObject().put("start", iso(start)).put("end", if (end != null) iso(end) else JSONObject.NULL)
        post(hubFor(ctx) + "/api/sync/breaks", token(ctx), JSONArray().put(row).toString())
    }

    private fun token(ctx: Context) = p(ctx).getString("device_token", "") ?: ""
    private fun hubFor(ctx: Context) = (p(ctx).getString("hub_url", "") ?: "").ifEmpty { hubUrl }

    // ── Endpoint dispatcher ──────────────────────────────────────────

    /** Returns a JSON string; throws ApiError for non-2xx. Runs on a background thread. */
    fun call(ctx: Context, path: String, body: JSONObject?): JSONObject = when (path) {
        "state" -> state(ctx)
        "provision" -> provision(ctx, body)
        "login" -> login(ctx, body)
        "logout" -> logout(ctx)
        "start" -> JSONObject().put("ok", true).put("needs_consent", true) // MainActivity shows the Android consent
        "stop" -> stop(ctx)
        "break" -> toggleBreak(ctx)
        "idle" -> JSONObject().put("ok", true)
        "quit", "open-permissions" -> JSONObject().put("ok", true)
        else -> err(404, "not found")
    }

    private fun state(ctx: Context): JSONObject {
        rollDay(ctx)
        val pr = p(ctx)
        val running = ScreenActivityService.isRunning()
        val sessionStart = pr.getLong("session_start", 0L)
        val breakStart = pr.getLong("break_start", 0L)
        val live = if (running && sessionStart > 0) (now() - sessionStart) / 1000 else 0
        val cap = JSONObject()
        if (ScreenActivityService.lastCaptureAt > 0) cap.put("last_success", iso(ScreenActivityService.lastCaptureAt))
        if (ScreenActivityService.lastError.isNotEmpty()) {
            cap.put("last_error", ScreenActivityService.lastError).put("last_error_at", iso(now()))
        }
        return JSONObject()
            .put("signed_in", token(ctx).isNotEmpty())
            .put("login", pr.getString("login", ""))
            .put("remember", pr.getBoolean("remember", true))
            .put("user_name", pr.getString("employee_name", ""))
            .put("company_name", pr.getString("company_name", ""))
            .put("version", BuildConfig.VERSION_NAME)
            .put("now", iso(now()))
            .put("clocked_in", running)
            .put("on_break", running && breakStart > 0)
            .put("today_seconds", pr.getLong("day_seconds", 0) + live)
            .put("break_seconds", pr.getLong("break_day_seconds", 0) + if (breakStart > 0) (now() - breakStart) / 1000 else 0)
            .put("current_start", if (running && sessionStart > 0) iso(sessionStart) else JSONObject.NULL)
            .put("break_start", if (breakStart > 0) iso(breakStart) else JSONObject.NULL)
            .put("idle", JSONObject.NULL)
            .put("screen_permission", "granted")
            .put("capture", cap)
            // Automatic (managed) device: never show manual Start/Stop/Break.
            .put("auto_mode", pr.getBoolean("managed", false) || running)
            .put("managed", pr.getBoolean("managed", false))
    }

    /** Automatic (managed) setup with an employer-issued device token — same as desktop. */
    private fun provision(ctx: Context, body: JSONObject?): JSONObject {
        val tok = body?.optString("token")?.trim().orEmpty()
        if (tok.isEmpty()) err(422, "Enter the device token.")
        val hb = JSONObject().put("status", "offline").put("platform", "android").put("app_version", BuildConfig.VERSION_NAME)
        val (code, _) = post("$hubUrl/api/heartbeat", tok, hb.toString())
            ?: err(502, "Can't reach the activity server. Check the connection.")
        if (code != 200) err(401, "Invalid or revoked device token.")
        var name = ""
        get("$hubUrl/api/sync/config", tok)?.let { (c, b) ->
            if (c == 200) name = try { JSONObject(b).getJSONObject("data").optString("employee_name") } catch (_: Exception) { "" }
        }
        p(ctx).edit().putString("hub_url", hubUrl).putString("device_token", tok).putBoolean("managed", true)
            .putString("employee_name", name).apply()
        return JSONObject().put("ok", true)
    }

    /** Manual setup: sign in with Hajir, then register this phone on the hub — same as desktop. */
    private fun login(ctx: Context, body: JSONObject?): JSONObject {
        val login = body?.optString("login")?.trim().orEmpty()
        val password = body?.optString("password").orEmpty()
        val remember = body?.optBoolean("remember", true) ?: true
        val companyId = body?.optLong("company_id", 0L) ?: 0L
        if (login.isEmpty() || password.isEmpty()) err(422, "Enter your email/phone and password.")
        val device = "${Build.MANUFACTURER} ${Build.MODEL}".trim()
        val pr = p(ctx)
        val devId = pr.getString("install_id", "") ?: ""
        val installId = devId.ifEmpty { "hajir-android-" + java.util.UUID.randomUUID().toString().replace("-", "") }
        pr.edit().putString("install_id", installId).apply()

        val lb = JSONObject().put("phone", login).put("password", password).put("device_token", installId)
            .put("name", device).put("type", "android").put("app_version", BuildConfig.VERSION_NAME)
        val (code, raw) = post("$apiUrl/candidate/login", null, lb.toString())
            ?: err(502, "Can't reach Hajir. Check your internet connection.")
        val access = try { JSONObject(raw).getJSONObject("data").getJSONObject("token").getString("access_token") } catch (_: Exception) { "" }
        if (code != 200 || access.isEmpty()) {
            val m = try { JSONObject(raw).optString("message") } catch (_: Exception) { "" }
            err(401, m.ifEmpty { "Incorrect email or password." })
        }
        val reg = JSONObject().put("company_id", companyId).put("device_name", device).put("platform", "android")
        val (rc, rb) = post("$hubUrl/api/device/register", access, reg.toString())
            ?: err(502, "Can't reach the activity server.")
        val j = try { JSONObject(rb) } catch (_: Exception) { JSONObject() }
        if (rc == 409) err(409, "Choose the company you're working for.", JSONObject().put("companies", j.optJSONArray("companies") ?: JSONArray()))
        if (rc != 200) err(rc, j.optString("message").ifEmpty { "Could not connect this device." })
        val d = j.getJSONObject("data")
        pr.edit()
            .putString("hub_url", hubUrl).putString("device_token", d.getString("token")).putBoolean("managed", false)
            .putString("company_name", d.optString("company_name")).putString("employee_name", d.optString("employee_name"))
            .putString("login", if (remember) login else "").putBoolean("remember", remember)
            .apply()
        return JSONObject().put("ok", true)
    }

    private fun stop(ctx: Context): JSONObject {
        if (ScreenActivityService.isRunning()) {
            ctx.startService(android.content.Intent(ctx, ScreenActivityService::class.java).setAction(ScreenActivityService.ACTION_STOP))
        }
        return JSONObject().put("ok", true)
    }

    private fun toggleBreak(ctx: Context): JSONObject {
        if (!ScreenActivityService.isRunning()) err(409, "Start tracking first.")
        val pr = p(ctx)
        return if (pr.getLong("break_start", 0L) > 0) {
            endBreakIfAny(ctx)
            JSONObject().put("ok", true).put("on_break", false)
        } else {
            val start = now()
            pr.edit().putLong("break_start", start).apply()
            syncBreak(ctx, start, null)
            JSONObject().put("ok", true).put("on_break", true)
        }
    }

    private fun logout(ctx: Context): JSONObject {
        stop(ctx)
        val pr = p(ctx)
        val keepLogin = if (pr.getBoolean("remember", true)) pr.getString("login", "") else ""
        val install = pr.getString("install_id", "")
        pr.edit().clear().putString("login", keepLogin).putString("install_id", install).apply()
        return JSONObject().put("ok", true)
    }

    // ── HTTP ─────────────────────────────────────────────────────────

    private fun post(url: String, bearer: String?, json: String) = request("POST", url, bearer, json)
    private fun get(url: String, bearer: String?) = request("GET", url, bearer, null)

    private fun request(method: String, url: String, bearer: String?, json: String?): Pair<Int, String>? {
        var c: HttpURLConnection? = null
        return try {
            c = (URL(url).openConnection() as HttpURLConnection).apply {
                requestMethod = method; connectTimeout = 15_000; readTimeout = 30_000
                setRequestProperty("Accept", "application/json")
                if (bearer != null) setRequestProperty("Authorization", "Bearer $bearer")
                if (json != null) { doOutput = true; setRequestProperty("Content-Type", "application/json") }
            }
            if (json != null) c.outputStream.use { it.write(json.toByteArray()) }
            val code = c.responseCode
            code to ((if (code < 400) c.inputStream else c.errorStream)?.bufferedReader()?.readText() ?: "")
        } catch (_: Exception) { null } finally { c?.disconnect() }
    }
}
