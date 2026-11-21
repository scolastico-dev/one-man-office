package dev.scolastico.omo.notifications

import android.app.Activity
import android.graphics.Color
import android.view.View
import android.view.ViewGroup
import android.webkit.WebView
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView

/** App-owned frame outside both documents. Only native view switching sets trust. */
class TrustedChrome(private val activity:Activity, private val setup:WebView) {
    val frame=LinearLayout(activity)
    val caption=TextView(activity)
    val connection=Button(activity)
    private var board:WebView?=null
    init {
        val parent=setup.parent as ViewGroup
        val index=parent.indexOfChild(setup)
        val layout=setup.layoutParams
        parent.removeView(setup)
        frame.orientation=LinearLayout.VERTICAL
        val padding=(4*activity.resources.displayMetrics.density).toInt()
        frame.setPadding(padding,padding,padding,padding)
        val header=LinearLayout(activity)
        caption.setTextColor(Color.WHITE)
        caption.setPadding(padding*2,padding*2,padding*2,padding*2)
        header.addView(caption,LinearLayout.LayoutParams(0,ViewGroup.LayoutParams.WRAP_CONTENT,1f))
        connection.text="Connection"
        connection.setOnClickListener {showSetup()}
        header.addView(connection)
        frame.addView(header)
        frame.addView(setup,LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,0,1f))
        parent.addView(frame,index,layout)
        showSetup()
    }
    fun showBoard(view:WebView) {
        board?.let { if(it!==view) {frame.removeView(it);it.destroy()} }
        board=view
        if(view.parent==null) frame.addView(view,LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,0,1f))
        setup.visibility=View.GONE
        view.visibility=View.VISIBLE
        caption.text="omo ui · Host content"
        frame.setBackgroundColor(Color.rgb(83,49,26))
        activity.title=caption.text
    }
    fun showSetup() {
        board?.visibility=View.GONE
        setup.visibility=View.VISIBLE
        caption.text="omo ui · Trusted Connection"
        frame.setBackgroundColor(Color.rgb(29,66,128))
        activity.title=caption.text
    }
    fun closeBoard() {
        board?.let {frame.removeView(it);it.stopLoading();it.destroy()}
        board=null
        showSetup()
    }
}
