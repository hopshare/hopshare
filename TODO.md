# TODO

## Running local Postgres
    podman run --detach \
    --name postgres \
    -e POSTGRES_USER=hopuser \
    -e POSTGRES_PASSWORD=hoppass \
    -e POSTGRES_DB=hopshare \
    -e POSTGRES_ADMIN_PASSWORD=adminpass \
    -v postgres_data:/var/lib/postgresql/data:Z \
    -p 5432:5432 \
    docker.io/library/postgres:17.7


## Bugs

* Private Hops not showing up in the "Give a Hop" list...they should be visible here
* Accepting Help on a Hop that has been Canceled should not be an error- just a message that the Hop was canceled already.
* An Organization Owner can request membership in their own Organization- this should be prevented
* Don't show the "Remove" button on the row for the primary Organization Owner when they go to the Manage Organization page
* Race condition when multiple users sign up at the same time with the same First and Last name. The first one in will win as username must be unique. There is some code in here to detect unique constraint violation but it's not working.
* Trying to view a private or non-Organization Hop Detail page shows the message "This page is only available to organization owners." - need to parameterize the unauthorized page message?
* The Public/Private toggle on Hop Details page updates the whole page/gets added to history when you toggle it...can we avoid that? Shall we just use a radio button?


## Now

* Header
    * Clicking the logo should take you to the main page, unless you are authenticated, then it should take you to the My HopShare dashboard
    * Need a "Help" page that walks through how to use the service

* Hop Detail Page
    * Members in the Organization should be able to offer to help for Pending Hops. If they have already offered to help, show an error banner saying you've already offered to help.


* My HopShare Dashboard
    * IDEA: Venmo as inspiration- both for individual 'dashboard' but also the 'feed' of activity in an Organization
    * Need a way to see:
        * All Hops I've offered to help with but not heard back yet
        * All Hops I've requested from others but not Canceled, or Completed
        * All Hops I've helped someone else with
        * All Hops for the Organization- preferably in a fun 'feed' manner
        * My current balance of Hours
    * Need a way to:
        * Ask for a Hop
        * Find Hops looking for help
        * Get a "bank statement" of all transactions

    * "Change..." for Organization should be a pulldown of all Organizations Member belongs to. 
    * At bottom of that card, have a link "Find an Organization..." that goes to Find Organization page.
    * Organization card doesn't work well with long names


* Organization "Wall"- closest thing to 'social media' feature- inspire others. A scrolling list of who's helped who recently. Or a "Tag Cloud" of who's helping who? VENMO

* Find Organization Page
    * Long Organization names do not fit into the search results- should put those into larger results- with ability to drill into details on the organization before asking to join.
* Joining an Organization should use messages
    * Send an information message to all Owners of an Organization when you request membership. The message body should contain a link that will take the Member directly to their 
    * Send yourself an information message that you requested membership in an Organization.
* Owners are moderators for listings- they can flag/delete inappropriate requests/comments
* Organizations need to have a readable URL for new joiners. A way for users and non-users to sign up quickly.
* Manage Skills on the Member profile page. We will need something for automatic matching...give it some thought. Skills should reside in the database- we can seed some starter ones, but it should grow over time- and be scoped within the organization. We can have these configured for new joiners via a wizard interface.
* Administrator page- see everything, do dangerous stuff. Link conditionally off header menu for Admin users.
* Add in basic monitoring (cron job calling script saving in sqlite):
    * net/http/pprof package (visualize performance)
    * runtime.MemStats / runtime.ReadMemStats() thru a /health endpoint on each golang process
    * select count(*) from pg_stat_activity; (database connections)
    * iostat to see iops levels
    * jq against Caddy logs for traffic levels


Change the "My Organization" panel of the "My Profile" page as follows:
* retitle it to "My Organizations"
* Show a list of all Organizations (logo and full name) the Member is associated with.
    * If the Member is an Owner of the Organization, make the name a clickable link that takes them to the Manage Organization page for that Organization.
    * If the Member is not an Owner of the Organization, follow each Organization row with a placeholder link that says "Leave..." which we will eventually use to let a Member leave that Organization.

## Later

* Create a 'celebration' page for the Organization?
* Make service/ExpireHelpRequests() asynchronous- we should start a goroutine that runs daily to clear these out (not only when the myhpopshare page is rendered).

Font Awesome- https://icon-sets.iconify.design/fa7-regular/page-2.html?keyword=font


