package com.istarvin.fetcharr.ui.common

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.widthIn
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

@Composable
fun useWideLayout(): Boolean {
    val windowSize = LocalWindowInfo.current.containerSize
    return with(LocalDensity.current) { windowSize.width.toDp() >= 600.dp }
}

@Composable
fun screenGutter(): Dp = if (useWideLayout()) 24.dp else 16.dp

/** Keeps task content readable on wide windows without constraining the app's navigation. */
@Composable
fun ScreenFrame(
    modifier: Modifier = Modifier,
    maxWidth: Dp = 720.dp,
    content: @Composable BoxScope.() -> Unit,
) {
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.TopCenter) {
        Box(
            Modifier.widthIn(max = maxWidth).fillMaxWidth().fillMaxHeight(),
            content = content,
        )
    }
}
