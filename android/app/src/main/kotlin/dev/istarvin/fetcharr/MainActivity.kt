package dev.istarvin.fetcharr

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import dagger.hilt.android.AndroidEntryPoint
import dev.istarvin.fetcharr.data.sync.SyncManager
import dev.istarvin.fetcharr.notifications.BackgroundController
import dev.istarvin.fetcharr.notifications.NotificationIntents
import dev.istarvin.fetcharr.ui.FetcharrAppUi
import dev.istarvin.fetcharr.ui.incoming.IncomingHub
import dev.istarvin.fetcharr.ui.navigation.NavigationEvents
import dev.istarvin.fetcharr.ui.navigation.OpenTarget
import dev.istarvin.fetcharr.ui.theme.FetcharrTheme
import javax.inject.Inject

@AndroidEntryPoint
class MainActivity : ComponentActivity() {
    @Inject
    lateinit var incomingHub: IncomingHub

    @Inject
    lateinit var syncManager: SyncManager

    @Inject
    lateinit var backgroundController: BackgroundController

    @Inject
    lateinit var navigationEvents: NavigationEvents

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // Only a fresh launch carries a new intent; after recreation (rotation, process restore) the same
        // intent would be delivered again and must not be handled twice.
        if (savedInstanceState == null) handle(intent)
        setContent {
            FetcharrTheme {
                FetcharrAppUi(navigationEvents.events)
            }
        }
    }

    override fun onStart() {
        super.onStart()
        syncManager.acquire(SYNC_REASON)
        backgroundController.onAppForegrounded()
    }

    override fun onStop() {
        syncManager.release(SYNC_REASON)
        super.onStop()
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
        // A notification tap names the tab to open; it carries no fetcharr:// data, so the router ignored it above.
        OpenTarget.fromExtra(intent.getStringExtra(NotificationIntents.EXTRA_OPEN))?.let(navigationEvents::open)
    }

    private companion object {
        const val EXTRA_PAYLOAD = "payload"
        const val SYNC_REASON = "ui"
    }
}
