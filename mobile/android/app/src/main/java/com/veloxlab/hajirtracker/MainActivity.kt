package com.veloxlab.hajirtracker

import android.annotation.SuppressLint
import android.app.Activity
import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
import android.Manifest
import android.content.pm.PackageManager.COMPONENT_ENABLED_STATE_DISABLED
import android.graphics.Color
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Bundle
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import org.json.JSONObject

/**
 * My Monitor for Android — automatic (device-token) tracking only.
 *
 * Flow: paste the device token once → the app asks for screen-capture consent →
 * tracking runs in the foreground service and the launcher icon is hidden. From
 * then on the app has no manual Start/Stop; it just runs in the background and
 * resumes on boot. The only screen is the one-time token setup (and the one-tap
 * resume after a reboot on non-managed phones, which Android requires).
 */
class MainActivity : Activity() {
    companion object {
        private const val REQ_CAPTURE = 4207
        private const val REQ_NOTIF = 4208
        private const val BASE = "https://tracker.hajir.local/app/"
    }

    private lateinit var web: WebView
    private var autoPrompted = false

    private val configured get() = !ScreenActivityService.prefs(this).getString("device_token", "").isNullOrEmpty()

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.statusBarColor = Color.parseColor("#22408B")
        web = WebView(this)
        setContentView(web)
        web.settings.javaScriptEnabled = true
        web.settings.domStorageEnabled = true
        web.webChromeClient = WebChromeClient()
        web.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(view: WebView, req: WebResourceRequest): WebResourceResponse? {
                val path = req.url.path ?: return null
                if (req.url.host == "tracker.hajir.local" && path == "/app/logo.svg")
                    return WebResourceResponse("image/svg+xml", "utf-8", assets.open("logo.svg"))
                return null
            }
        }
        web.addJavascriptInterface(Bridge(), "HajirNative")
        web.loadDataWithBaseURL(BASE, page(), "text/html", "utf-8", null)

        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQ_NOTIF)

        maybeAutoResume()
    }

    /** A configured device that isn't tracking (fresh launch, or after a reboot)
     *  asks for the single capture consent straight away — no button to press. */
    private fun maybeAutoResume() {
        if (configured && !ScreenActivityService.isRunning() && !autoPrompted) {
            autoPrompted = true
            web.postDelayed({ requestCapture() }, 400)
        }
    }

    /** Desktop app.html, forced to the token-only setup and automatic wording. */
    private fun page(): String {
        var html = assets.open("app.html").bufferedReader().readText()
        val replace = listOf(
            // First run goes straight to the device-token screen (no Manual choice).
            "show('vSetup');" to "show('vToken');",
            "Automatic setup" to "Set up this phone",
            "Paste the device token from your Hajir dashboard (Connect a device)." to
                "Paste the device token from your dashboard. This phone will then track automatically in the background.",
            "Allow <b>MyMonitor</b> in System Settings → Privacy &amp; Security → Screen Recording, then quit and reopen the app." to
                "Allow screen capture when Android asks. Tracking then runs automatically.",
            // Strip all branding text from the tracker screen.
            "<title>Hajir Tracker</title>" to "<title>My Monitor</title>",
            "alt=\"Hajir\">" to "alt=\"\" style=\"display:none\">",
            "Quit Hajir Tracker? Your tracking will stop." to "Quit? Tracking will stop.",
            "Hajir Tracker has quit. You can close this window." to "Stopped. You can close this window.",
        )
        for ((a, b) in replace) html = html.replace(a, b)
        val shim = """
<script>
(function(){
  var orig = window.fetch.bind(window);
  window.fetch = function(u, o){
    if (typeof u === 'string' && u.indexOf('/app/api/') === 0) {
      var path = u.slice(9), body = (o && o.body) ? o.body : '';
      return new Promise(function(resolve){
        setTimeout(function(){
          var r = JSON.parse(HajirNative.call(path, body));
          resolve(new Response(r.body, { status: r.status, headers: { 'Content-Type': 'application/json' } }));
        }, 0);
      });
    }
    return orig(u, o);
  };
})();
</script>"""
        return html.replaceFirst("<head>", "<head>$shim")
    }

    inner class Bridge {
        @JavascriptInterface
        fun call(path: String, body: String): String {
            val json = try { if (body.isBlank()) null else JSONObject(body) } catch (_: Exception) { null }
            return try {
                val out = Agent.call(this@MainActivity, path, json)
                if (path == "provision") runOnUiThread { requestCapture() } // configured → start at once
                JSONObject().put("status", 200).put("body", out.toString()).toString()
            } catch (e: Agent.ApiError) {
                JSONObject().put("status", e.status).put("body", e.body.toString()).toString()
            } catch (e: Exception) {
                JSONObject().put("status", 500).put("body", JSONObject().put("error", e.message ?: "Something went wrong").toString()).toString()
            }
        }
    }

    private fun requestCapture() {
        if (ScreenActivityService.isRunning()) return
        val mpm = getSystemService(MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
        @Suppress("DEPRECATION")
        startActivityForResult(mpm.createScreenCaptureIntent(), REQ_CAPTURE)
    }

    @Deprecated("MediaProjection consent result")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQ_CAPTURE) return
        if (resultCode != RESULT_OK || data == null) {
            toast("Screen capture permission is needed to track.")
            return
        }
        val i = Intent(this, ScreenActivityService::class.java)
            .putExtra(ScreenActivityService.EXTRA_RESULT_CODE, resultCode)
            .putExtra(ScreenActivityService.EXTRA_RESULT_DATA, data)
        if (Build.VERSION.SDK_INT >= 26) startForegroundService(i) else startService(i)
        hideLauncherIcon()
        // Running in the background now; close the window.
        web.postDelayed({ moveTaskToBack(true) }, 1200)
    }

    /** After the device is set up and tracking, remove the app from the launcher. */
    private fun hideLauncherIcon() {
        try {
            packageManager.setComponentEnabledSetting(
                ComponentName(this, "com.veloxlab.hajirtracker.Launcher"),
                COMPONENT_ENABLED_STATE_DISABLED, PackageManager.DONT_KILL_APP
            )
        } catch (_: Exception) {}
    }

    private fun toast(m: String) =
        web.evaluateJavascript("(function(){var t=document.getElementById('toast');if(t){t.textContent=${JSONObject.quote(m)};t.classList.add('show');setTimeout(function(){t.classList.remove('show')},2600)}})()", null)

    override fun onResume() {
        super.onResume()
        if (::web.isInitialized) {
            web.evaluateJavascript("window.hajirRefresh && window.hajirRefresh()", null)
            maybeAutoResume()
        }
    }

    @Deprecated("Back navigation")
    override fun onBackPressed() {
        moveTaskToBack(true) // keep tracking; don't destroy the activity
    }
}
