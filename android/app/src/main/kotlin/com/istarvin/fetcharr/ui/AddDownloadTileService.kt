package com.istarvin.fetcharr.ui

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.Intent
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import com.istarvin.fetcharr.MainActivity
import com.istarvin.fetcharr.ui.incoming.IntentRouter

/** Quick Settings tile that opens the Add screen, for pasting a link copied in another app. */
class AddDownloadTileService : TileService() {
    override fun onStartListening() {
        qsTile?.apply {
            state = Tile.STATE_INACTIVE
            updateTile()
        }
    }

    override fun onClick() {
        if (isLocked) unlockAndRun(::openAdd) else openAdd()
    }

    // The Intent overload is only used below API 34, where the PendingIntent one does not exist.
    @SuppressLint("StartActivityAndCollapseDeprecated")
    private fun openAdd() {
        val intent = Intent(this, MainActivity::class.java)
            .setAction(IntentRouter.ACTION_ADD_DOWNLOAD)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startActivityAndCollapse(PendingIntent.getActivity(this, 0, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(intent)
        }
    }
}
