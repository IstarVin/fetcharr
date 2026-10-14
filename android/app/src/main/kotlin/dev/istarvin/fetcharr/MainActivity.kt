package dev.istarvin.fetcharr

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import dagger.hilt.android.AndroidEntryPoint
import dev.istarvin.fetcharr.ui.FetcharrAppUi
import dev.istarvin.fetcharr.ui.incoming.IncomingHub
import dev.istarvin.fetcharr.ui.theme.FetcharrTheme
import javax.inject.Inject

@AndroidEntryPoint
class MainActivity : ComponentActivity() {
    @Inject
    lateinit var incomingHub: IncomingHub

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // Only a fresh launch carries a new intent; after recreation (rotation, process restore) the same
        // intent would be delivered again and must not be handled twice.
        if (savedInstanceState == null) handle(intent)
        setContent {
            FetcharrTheme {
                FetcharrAppUi()
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handle(intent)
    }

    /** Thin extraction only; all decisions are made by [IncomingHub] / `IntentRouter`. Never log the values. */
    private fun handle(intent: Intent) {
        // Relaunching from Recents re-delivers the original intent; it was already handled.
        if (intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY != 0) return
        incomingHub.handleIntent(
            action = intent.action,
            dataString = intent.dataString,
            extraText = intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString(),
            payloadExtra = intent.getStringExtra(EXTRA_PAYLOAD),
        )
    }

    private companion object {
        const val EXTRA_PAYLOAD = "payload"
    }
}
