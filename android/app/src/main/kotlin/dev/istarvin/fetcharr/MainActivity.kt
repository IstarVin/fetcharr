package dev.istarvin.fetcharr

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import dagger.hilt.android.AndroidEntryPoint
import dev.istarvin.fetcharr.ui.FetcharrAppUi
import dev.istarvin.fetcharr.ui.theme.FetcharrTheme

@AndroidEntryPoint
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            FetcharrTheme {
                FetcharrAppUi()
            }
        }
    }
}
